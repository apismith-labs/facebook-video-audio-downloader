package com.apismith.facebookdownload;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * Media download layer: resumable HTTP transfers to disk.
 *
 * <p>A signed fbcdn link is short-lived, so download right after the Run succeeds. Downloads resume
 * with {@code Range}, verify the byte count and SHA-256, and are renamed into place only when
 * complete.
 */
public final class Media {

    public static final int CHUNK_SIZE = 1 << 20;
    public static final Duration DEFAULT_TIMEOUT = Duration.ofSeconds(60);
    public static final int DEFAULT_MAX_ATTEMPTS = 3;
    private static final List<Integer> RETRYABLE = List.of(408, 425, 429, 500, 502, 503, 504);

    private Media() {
    }

    /** A download failure. {@code status} is the HTTP status when a response was received. */
    public static final class MediaException extends Exception {
        private static final long serialVersionUID = 1L;
        public final int status;
        public final boolean retryable;

        public MediaException(String message) {
            this(message, 0, false);
        }

        public MediaException(String message, int status, boolean retryable) {
            super(message);
            this.status = status;
            this.retryable = retryable;
        }
    }

    /** One downloaded or reused file. */
    public record Asset(String kind, String path, long bytes, String sha256,
                        String contentType, String sourceHost, boolean skipped) {
        public Map<String, Object> toMap() {
            Map<String, Object> map = new LinkedHashMap<>();
            map.put("kind", kind);
            map.put("path", path);
            map.put("bytes", bytes);
            map.put("sha256", sha256);
            map.put("contentType", contentType);
            map.put("sourceHost", sourceHost);
            map.put("skipped", skipped);
            return map;
        }
    }

    /** Callback for progress reporting. */
    public interface Progress {
        void update(String kind, long written, long total);
    }

    /** Download tuning. */
    public static final class Options {
        public String kind = "video";
        public Duration timeout = DEFAULT_TIMEOUT;
        public int maxAttempts = DEFAULT_MAX_ATTEMPTS;
        public boolean force;
        public Progress progress;
        public HttpClient client;
    }

    /** Returns scheme://host/path, dropping the signed query string. */
    public static String redactMediaUrl(String value) {
        if (value == null || value.isEmpty()) {
            return "";
        }
        try {
            URI uri = URI.create(value);
            if (uri.getHost() == null) {
                return "[unparseable media url]";
            }
            StringBuilder builder = new StringBuilder();
            builder.append(uri.getScheme()).append("://").append(uri.getRawAuthority());
            if (uri.getRawPath() != null) {
                builder.append(uri.getRawPath());
            }
            return builder.toString();
        } catch (IllegalArgumentException error) {
            return "[unparseable media url]";
        }
    }

    /** Host only, for provenance records. */
    public static String mediaHost(String value) {
        try {
            String host = URI.create(value).getHost();
            return host == null ? "" : host.toLowerCase().replaceAll("\\.$", "");
        } catch (IllegalArgumentException error) {
            return "";
        }
    }

    static String sha256Of(Path path) throws IOException {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            try (InputStream stream = Files.newInputStream(path)) {
                byte[] buffer = new byte[CHUNK_SIZE];
                int read;
                while ((read = stream.read(buffer)) > 0) {
                    digest.update(buffer, 0, read);
                }
            }
            StringBuilder hex = new StringBuilder();
            for (byte value : digest.digest()) {
                hex.append(String.format("%02x", value));
            }
            return hex.toString();
        } catch (java.security.NoSuchAlgorithmException error) {
            throw new IOException("SHA-256 is unavailable", error);
        }
    }

    private static long parseContentRangeTotal(String header) {
        if (header == null) {
            return -1;
        }
        int slash = header.lastIndexOf('/');
        if (slash < 0) {
            return -1;
        }
        try {
            return Long.parseLong(header.substring(slash + 1).trim());
        } catch (NumberFormatException error) {
            return -1;
        }
    }

    /**
     * Download {@code url} to {@code destination}, resuming a {@code .part} file when possible.
     * An existing destination is returned with {@code skipped=true} unless {@code force} is set.
     */
    public static Asset download(String url, Path destination, Options options)
            throws MediaException, IOException, InterruptedException {
        Options settings = options == null ? new Options() : options;
        if (url == null || !(url.startsWith("https://") || url.startsWith("http://"))) {
            throw new MediaException("media URL must be an absolute http(s) URL");
        }
        String kind = settings.kind == null || settings.kind.isEmpty() ? "video" : settings.kind;
        String host = mediaHost(url);
        HttpClient client = settings.client != null
                ? settings.client
                : HttpClient.newBuilder().connectTimeout(settings.timeout).followRedirects(HttpClient.Redirect.NORMAL).build();

        if (!settings.force && Files.isRegularFile(destination)) {
            return new Asset(kind, destination.toString(), Files.size(destination), sha256Of(destination),
                    null, host, true);
        }

        Path parent = destination.getParent();
        if (parent != null) {
            Files.createDirectories(parent);
        }
        Path part = Path.of(destination.toString() + ".part");
        if (settings.force) {
            Files.deleteIfExists(part);
            Files.deleteIfExists(destination);
        }

        MediaException lastError = null;
        for (int attempt = 0; attempt < settings.maxAttempts; attempt++) {
            long resumeFrom = Files.isRegularFile(part) ? Files.size(part) : 0;
            try {
                return attemptDownload(client, url, destination, part, resumeFrom, kind, host, settings);
            } catch (MediaException error) {
                lastError = error;
                if (!error.retryable || attempt + 1 >= settings.maxAttempts) {
                    throw error;
                }
            }
            Thread.sleep(Math.min(1L << attempt, 8L) * 1000L);
        }
        throw lastError == null ? new MediaException("media download failed") : lastError;
    }

    private static Asset attemptDownload(HttpClient client, String url, Path destination, Path part,
                                         long resumeFrom, String kind, String host, Options settings)
            throws MediaException, IOException, InterruptedException {
        HttpRequest.Builder builder = HttpRequest.newBuilder(URI.create(url)).GET().timeout(settings.timeout);
        if (resumeFrom > 0) {
            builder.header("Range", "bytes=" + resumeFrom + "-");
        }

        HttpResponse<InputStream> response;
        try {
            response = client.send(builder.build(), HttpResponse.BodyHandlers.ofInputStream());
        } catch (IOException error) {
            throw new MediaException("media download transport failure: " + error.getClass().getSimpleName(), 0, true);
        }
        int status = response.statusCode();

        if (status == 416 && resumeFrom > 0) {
            long total = parseContentRangeTotal(response.headers().firstValue("Content-Range").orElse(null));
            if (total >= 0 && Files.isRegularFile(part) && Files.size(part) == total) {
                Files.move(part, destination, StandardCopyOption.REPLACE_EXISTING);
                return new Asset(kind, destination.toString(), total, sha256Of(destination), null, host, false);
            }
        }
        if (status != 200 && status != 206) {
            try (InputStream ignored = response.body()) {
                // Drain so the connection can be reused.
            } catch (IOException ignored) {
                // The status is what matters here.
            }
            throw new MediaException("media server returned HTTP " + status, status, RETRYABLE.contains(status));
        }

        boolean append = resumeFrom > 0 && status == 206;
        Optional<String> declared = response.headers().firstValue("Content-Length");
        long contentLength = declared.isPresent() ? Long.parseLong(declared.get().trim()) : -1;
        long total = parseContentRangeTotal(response.headers().firstValue("Content-Range").orElse(null));
        long expected = total;
        if (expected < 0) {
            expected = append && contentLength >= 0 ? resumeFrom + contentLength : contentLength;
        }
        String contentType = response.headers().firstValue("Content-Type").orElse(null);

        long written = append ? resumeFrom : 0;
        try (InputStream stream = response.body();
             OutputStream output = Files.newOutputStream(part,
                     append ? java.nio.file.StandardOpenOption.APPEND
                             : java.nio.file.StandardOpenOption.CREATE,
                     append ? java.nio.file.StandardOpenOption.WRITE
                             : java.nio.file.StandardOpenOption.TRUNCATE_EXISTING)) {
            byte[] buffer = new byte[CHUNK_SIZE];
            int read;
            while ((read = stream.read(buffer)) > 0) {
                output.write(buffer, 0, read);
                written += read;
                if (settings.progress != null) {
                    settings.progress.update(kind, written, expected);
                }
            }
        } catch (IOException error) {
            throw new MediaException("media download transport failure: " + error.getClass().getSimpleName(), 0, true);
        }

        long produced = Files.size(part);
        if (expected >= 0 && produced != expected) {
            throw new MediaException("truncated download: expected " + expected + " bytes, wrote " + produced, 0, true);
        }
        Files.move(part, destination, StandardCopyOption.REPLACE_EXISTING);
        return new Asset(kind, destination.toString(), produced, sha256Of(destination), contentType, host, false);
    }

    /** Maps a raw audio.mp4 to the requested container. */
    public static Path audioTarget(Path destination, String audioFormat) throws MediaException {
        switch (audioFormat) {
            case "raw":
                return destination;
            case "m4a":
            case "mp3": {
                String name = destination.getFileName().toString();
                int dot = name.lastIndexOf('.');
                String base = dot > 0 ? name.substring(0, dot) : name;
                return destination.resolveSibling(base + "." + audioFormat);
            }
            default:
                throw new MediaException("audioFormat must be one of: raw, m4a, mp3");
        }
    }

    /** Builds the ffmpeg argument list for an audio container change. */
    public static List<String> ffmpegArgs(Path source, Path target, String audioFormat) throws MediaException {
        List<String> args = new ArrayList<>();
        args.add("ffmpeg");
        args.add("-nostdin");
        args.add("-y");
        args.add("-loglevel");
        args.add("error");
        args.add("-i");
        args.add(source.toString());
        args.add("-vn");
        if ("m4a".equals(audioFormat)) {
            args.add("-c:a");
            args.add("copy");
            args.add("-movflags");
            args.add("+faststart");
        } else if ("mp3".equals(audioFormat)) {
            args.add("-c:a");
            args.add("libmp3lame");
            args.add("-b:a");
            args.add("192k");
        } else {
            throw new MediaException("transcode is only defined for m4a and mp3");
        }
        args.add(target.toString());
        return args;
    }

    /** Converts an audio-only MP4 into m4a or mp3 using ffmpeg. */
    public static Path transcodeAudio(Path source, Path target, String audioFormat)
            throws MediaException, IOException, InterruptedException {
        if (!Files.isRegularFile(source)) {
            throw new MediaException("audio source is missing: " + source);
        }
        if (target.getParent() != null) {
            Files.createDirectories(target.getParent());
        }
        Process process = new ProcessBuilder(ffmpegArgs(source, target, audioFormat))
                .redirectErrorStream(true)
                .start();
        String output;
        try (InputStream stream = process.getInputStream()) {
            output = new String(stream.readAllBytes(), StandardCharsets.UTF_8).trim();
        }
        int code = process.waitFor();
        if (code != 0) {
            String[] lines = output.split("\n");
            String detail = lines.length == 0 || lines[lines.length - 1].isBlank()
                    ? "exit code " + code
                    : lines[lines.length - 1].trim();
            if (detail.length() > 160) {
                detail = detail.substring(0, 160);
            }
            throw new MediaException("ffmpeg failed: " + detail);
        }
        return target;
    }
}
