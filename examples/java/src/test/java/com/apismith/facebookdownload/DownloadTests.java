package com.apismith.facebookdownload;

import com.sun.net.httpserver.HttpServer;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Offline tests for the download layer: media transfers, layout rules, and the end-to-end pipeline
 * against a fake Actor. No network access, no token, no billable Run.
 */
public final class DownloadTests {
    private static int passed;

    private DownloadTests() {
    }

    public static void main(String[] args) throws Exception {
        layoutTests();
        segmentTests();
        mediaTests();
        pipelineTests();
        System.out.println(passed + " assertions passed");
    }

    private static void layoutTests() {
        check("a-day-at-the-beach".equals(Layout.slugify("A Day at the Beach!", 0)), "ascii slug");
        check("".equals(Layout.slugify("海边的一天", 0)), "non-ascii degrades to empty");
        check("trip-2026".equals(Layout.slugify("Trip 2026 — 海边", 0)), "mixed slug keeps ascii");
        // Digits split by CJK punctuation collapse, matching the Python/Node/Go clients. A dash
        // here would fork the output folder name between clients.
        check("51531".equals(Layout.slugify("韓國瑜 稱有人想當美國第51州、日本第5大島、中國第31省", 0)),
                "cjk separators never create a dash");
        check("51531".equals(Layout.slugify("第51州、日本第5大島、中國第31省", 0)), "cjk only digits");
        check("watch-123".equals(Layout.videoIdentity("https://www.facebook.com/watch/?v=123", "watch")),
                "watch identity");
        check("video-123".equals(Layout.videoIdentity("https://www.facebook.com/video.php?v=123", "video_php")),
                "video.php identity");
        check("page-pagename-123".equals(Layout.videoIdentity(
                "https://www.facebook.com/PageName/videos/123/", "page_video")), "page video identity");
        check("video-123".equals(Layout.videoIdentity("https://www.facebook.com/videos/123/", "video")),
                "videos identity");
        check("reel-123".equals(Layout.videoIdentity("https://www.facebook.com/reel/123/", "reel")), "reel identity");
        check("share-r-AbC-1".equals(Layout.videoIdentity(
                "https://www.facebook.com/share/r/AbC-1/", "share")), "share identity keeps case");
        check("short-AbC123".equals(Layout.videoIdentity("https://fb.watch/AbC123/", "short_link")),
                "short link identity");
        check("reel-9-sunset-timelapse".equals(Layout.folderName(
                "https://www.facebook.com/reel/9/", "reel", "Sunset Timelapse")), "folder name");

        Map<String, Object> item = new LinkedHashMap<>();
        item.put("videoUrl", "https://scontent.xx.fbcdn.net/v/a.mp4?oh=1&oe=2&sig=3");
        item.put("text", "hello");
        Map<String, Object> cleaned = Layout.sanitizeItem(item, false);
        check("https://scontent.xx.fbcdn.net/v/a.mp4".equals(cleaned.get("videoUrl")), "signed query stripped");
        check("hello".equals(cleaned.get("text")), "unrelated fields survive");
        check(String.valueOf(item.get("videoUrl")).contains("sig=3"), "source item not mutated");
        check(String.valueOf(Layout.sanitizeItem(item, true).get("videoUrl")).contains("sig=3"),
                "opt-in keeps the URL");
    }

    private static void segmentTests() {
        List<Map<String, Object>> raw = new ArrayList<>();
        raw.add(Map.of("start", 0.0, "end", 1.5, "text", "a"));
        raw.add(Map.of("start", 2.0, "end", 1.0, "text", "reversed"));
        raw.add(Map.of("start", "0", "end", 1.0, "text", "string start"));
        raw.add(Map.of("start", 1.0, "end", 2.0, "text", 5));
        List<Map<String, Object>> kept = Layout.transcriptSegments(raw);
        check(kept.size() == 1, "only well-formed segments survive, got " + kept.size());
        check("a".equals(kept.get(0).get("text")), "kept segment text");
    }

    private static void mediaTests() throws Exception {
        byte[] body = payload(8192);
        HttpServer server = startServer(body, true);
        try {
            Path dir = Files.createTempDirectory("fbdl-java");
            Path target = dir.resolve("video.mp4");

            Media.Asset asset = Media.download("http://127.0.0.1:" + server.getAddress().getPort() + "/media.mp4",
                    target, new Media.Options());
            check(asset.bytes() == body.length, "full download size");
            check(Arrays.equals(Files.readAllBytes(target), body), "full download content");
            check(!Files.exists(Path.of(target + ".part")), ".part renamed away");

            Path resumed = dir.resolve("resume.mp4");
            Files.write(Path.of(resumed + ".part"), Arrays.copyOfRange(body, 0, 3000));
            Media.download("http://127.0.0.1:" + server.getAddress().getPort() + "/media.mp4",
                    resumed, new Media.Options());
            check(Arrays.equals(Files.readAllBytes(resumed), body), "resumed download content");

            Media.Asset skipped = Media.download("http://127.0.0.1:9/never.mp4", target, new Media.Options());
            check(skipped.skipped(), "existing file is skipped");

            check("https://scontent.xx.fbcdn.net/v/a.mp4".equals(
                    Media.redactMediaUrl("https://scontent.xx.fbcdn.net/v/a.mp4?oh=1&sig=2")), "redaction");
            check("[unparseable media url]".equals(Media.redactMediaUrl("nonsense")), "unparseable marker");

            check(Media.ffmpegArgs(Path.of("a.mp4"), Path.of("a.m4a"), "m4a").contains("copy"),
                    "m4a uses stream copy");
            check(Media.ffmpegArgs(Path.of("a.mp4"), Path.of("a.mp3"), "mp3").contains("libmp3lame"),
                    "mp3 re-encodes");
            check("audio.m4a".equals(Media.audioTarget(Path.of("out/audio.mp4"), "m4a").getFileName().toString()),
                    "audio target extension");
        } finally {
            server.stop(0);
        }
    }

    private static void pipelineTests() throws Exception {
        byte[] body = payload(4096);
        HttpServer server = startServer(body, true);
        try {
            String mediaUrl = "http://127.0.0.1:" + server.getAddress().getPort() + "/media.mp4";
            Path out = Files.createTempDirectory("fbdl-java-out");

            Map<String, Object> item = new LinkedHashMap<>();
            item.put("url", "https://www.facebook.com/watch/?v=1115932820735409");
            item.put("title", "Q & A about sleeping");
            item.put("videoUrl", mediaUrl);
            item.put("audioUrl", mediaUrl);
            item.put("duration", 43.5);
            item.put("nickname", "Dr. Shu");
            item.put("text", "Hello, Dr.");
            item.put("segments", List.of(Map.of("start", 0.0, "end", 1.5, "text", "Hello, Dr.")));

            Downloader.Options options = new Downloader.Options();
            options.outDir = out.toString();
            options.run = runOptions(fakeTransport(List.of(item)));

            Downloader.Outcome outcome = Downloader.downloadOne(
                    "https://www.facebook.com/watch/?v=1115932820735409", options);
            check(outcome.exitCode() == 0, "download exit code");
            Path folder = Path.of(String.valueOf(outcome.report().get("outputDir")));
            check(folder.getFileName().toString().equals("watch-1115932820735409-q-a-about-sleeping"),
                    "folder name, got " + folder.getFileName());
            check(Files.isRegularFile(folder.resolve("video.mp4")), "video written");
            check(Files.isRegularFile(folder.resolve("manifest.json")), "manifest written");
            check(Files.readString(folder.resolve("transcript.txt")).equals("Hello, Dr.\n"), "transcript text");
            check(!Files.exists(folder.resolve("audio.mp4")), "audio skipped by default");
            Map<String, Object> metadata = Json.asObject(Json.parse(
                    Files.readString(folder.resolve("metadata.json"))));
            check(mediaUrl.equals(metadata.get("videoUrl")), "unsigned metadata URL unchanged");
            Map<String, Object> manifest = Json.asObject(Json.parse(
                    Files.readString(folder.resolve("manifest.json"))));
            check(((Number) manifest.get("schemaVersion")).intValue() == Layout.MANIFEST_SCHEMA_VERSION,
                    "manifest schema version");
            check("run-1".equals(manifest.get("runId")), "manifest run id");

            Map<String, Object> silent = new LinkedHashMap<>(item);
            silent.put("text", null);
            silent.put("segments", List.of());
            Downloader.Options silentOptions = new Downloader.Options();
            silentOptions.outDir = out.toString();
            silentOptions.run = runOptions(fakeTransport(List.of(silent)));
            Downloader.Outcome silentOutcome = Downloader.downloadOne(
                    "https://www.facebook.com/watch/?v=1115932820735409", silentOptions);
            check(silentOutcome.exitCode() == 0, "silent video still downloads");
            check("no_transcript".equals(silentOutcome.report().get("status")), "silent status");

            Map<String, Object> failing = new LinkedHashMap<>(item);
            failing.put("errMsg", "no audio url found");
            failing.put("text", null);
            Downloader.Options failingOptions = new Downloader.Options();
            failingOptions.outDir = out.toString();
            failingOptions.run = runOptions(fakeTransport(List.of(failing)));
            Downloader.Outcome failingOutcome = Downloader.downloadOne(
                    "https://www.facebook.com/watch/?v=1115932820735409", failingOptions);
            check(failingOutcome.exitCode() == 4, "actor_error exit code, got " + failingOutcome.exitCode());
            Path failedFolder = Path.of(String.valueOf(failingOutcome.report().get("outputDir")));
            check(Files.isRegularFile(failedFolder.resolve("metadata.json")), "metadata kept on failure");

            List<String> conversions = new ArrayList<>();
            Downloader.Outcome audioOutcome = Downloader.downloadOne(
                    "https://www.facebook.com/watch/?v=1115932820735409",
                    audioOptions(out.toString(), fakeTransport(List.of(item)), conversions));
            check(audioOutcome.exitCode() == 0, "audio download exit code");
            Path audioFolder = Path.of(String.valueOf(audioOutcome.report().get("outputDir")));
            Path converted = audioFolder.resolve("audio.m4a");
            check(conversions.size() == 1, "conversion ran once, got " + conversions.size());
            check(Files.readString(converted).equals("fake-1"), "converted container written");
            check(assetKinds(audioOutcome).equals(List.of("audio", "audio-converted")), "audio assets");

            Downloader.Outcome rerunOutcome = Downloader.downloadOne(
                    "https://www.facebook.com/watch/?v=1115932820735409",
                    audioOptions(out.toString(), fakeTransport(List.of(item)), conversions));
            check(conversions.size() == 1, "a plain rerun keeps the container");
            check(assetKinds(rerunOutcome).equals(List.of("audio", "audio-converted")), "rerun assets");
            check(assetSkipped(rerunOutcome, 1), "a reused container must be reported as skipped");

            // --force re-downloads audio.mp4, so audio.m4a has to be rebuilt from the new bytes.
            Downloader.Options forcedOptions = audioOptions(
                    out.toString(), fakeTransport(List.of(item)), conversions);
            forcedOptions.force = true;
            Downloader.Outcome forcedOutcome = Downloader.downloadOne(
                    "https://www.facebook.com/watch/?v=1115932820735409", forcedOptions);
            check(forcedOutcome.exitCode() == 0, "forced exit code");
            check(conversions.size() == 2, "--force re-derives the container, got " + conversions.size());
            check(Files.readString(converted).equals("fake-2"), "converted content refreshed");
            check(!assetSkipped(forcedOutcome, 1), "a forced rebuild must not be reported as skipped");

            Downloader.Options badOptions = new Downloader.Options();
            badOptions.outDir = out.toString();
            FakeTransport unused = fakeTransport(List.of());
            badOptions.run = runOptions(unused);
            Downloader.Outcome badOutcome = Downloader.downloadOne("https://example.com/nope", badOptions);
            check(badOutcome.exitCode() == 2, "invalid URL exits 2");
            check(unused.count("POST") == 0, "no Run created for an invalid URL");
        } finally {
            server.stop(0);
        }
    }

    /**
     * Audio-only options whose transcoder is a stand-in for ffmpeg: it tags the output with the
     * call number so a rebuilt container is distinguishable from a stale one.
     */
    private static Downloader.Options audioOptions(String outDir, FakeTransport transport, List<String> log) {
        Downloader.Options options = new Downloader.Options();
        options.outDir = outDir;
        options.wantVideo = false;
        options.wantAudio = true;
        options.audioFormat = "m4a";
        options.run = runOptions(transport);
        options.transcoder = (source, target, format) -> {
            log.add(format);
            Files.write(target, ("fake-" + log.size()).getBytes(StandardCharsets.UTF_8));
        };
        return options;
    }

    private static boolean assetSkipped(Downloader.Outcome outcome, int index) {
        List<?> assets = (List<?>) outcome.report().get("assets");
        return Boolean.TRUE.equals(((Map<?, ?>) assets.get(index)).get("skipped"));
    }

    private static List<String> assetKinds(Downloader.Outcome outcome) {
        List<String> kinds = new ArrayList<>();
        for (Object entry : (List<?>) outcome.report().get("assets")) {
            kinds.add(String.valueOf(((Map<?, ?>) entry).get("kind")));
        }
        return kinds;
    }

    private static TranscriptClient.Options runOptions(ReliableActor.Transport transport) {
        TranscriptClient.Options options = new TranscriptClient.Options();
        options.token = "offline-test-token";
        options.transport = transport;
        options.timeoutSeconds = 10;
        return options;
    }

    private static byte[] payload(int size) {
        byte[] body = new byte[size];
        for (int index = 0; index < size; index++) {
            body[index] = (byte) (index % 256);
        }
        return body;
    }

    private static HttpServer startServer(byte[] body, boolean allowRange) throws IOException {
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/media.mp4", exchange -> {
            String range = exchange.getRequestHeaders().getFirst("Range");
            int start = 0;
            if (allowRange && range != null && range.startsWith("bytes=")) {
                String value = range.substring(6).split("-", -1)[0];
                start = value.isEmpty() ? 0 : Integer.parseInt(value);
            }
            if (start >= body.length) {
                exchange.getResponseHeaders().add("Content-Range", "bytes */" + body.length);
                exchange.sendResponseHeaders(416, -1);
                exchange.close();
                return;
            }
            byte[] chunk = Arrays.copyOfRange(body, start, body.length);
            exchange.getResponseHeaders().add("Content-Type", "video/mp4");
            if (start > 0) {
                exchange.getResponseHeaders().add("Content-Range",
                        "bytes " + start + "-" + (body.length - 1) + "/" + body.length);
            }
            exchange.sendResponseHeaders(start > 0 ? 206 : 200, chunk.length);
            try (OutputStream stream = exchange.getResponseBody()) {
                stream.write(chunk);
            }
        });
        server.start();
        return server;
    }

    private static final class FakeTransport implements ReliableActor.Transport {
        private final List<String> calls = new ArrayList<>();
        private final List<Object> items;

        FakeTransport(List<Object> items) {
            this.items = items;
        }

        @Override
        public Object request(String method, String path, Object body, Map<String, String> query) {
            calls.add(method + " " + path);
            if ("POST".equals(method)) {
                Map<String, Object> data = new LinkedHashMap<>();
                data.put("id", "run-1");
                data.put("status", "READY");
                data.put("defaultDatasetId", "ds-1");
                data.put("buildNumber", ReliableActor.APPROVED_BUILD_NUMBER);
                data.put("buildId", ReliableActor.APPROVED_BUILD_ID);
                return Map.of("data", data);
            }
            if (path.startsWith("/actor-runs/")) {
                Map<String, Object> data = new LinkedHashMap<>();
                data.put("id", "run-1");
                data.put("status", "SUCCEEDED");
                data.put("defaultDatasetId", "ds-1");
                data.put("buildNumber", ReliableActor.APPROVED_BUILD_NUMBER);
                data.put("buildId", ReliableActor.APPROVED_BUILD_ID);
                return Map.of("data", data);
            }
            if (path.startsWith("/datasets/")) {
                return items;
            }
            throw new IllegalStateException("unexpected path " + path);
        }

        long count(String method) {
            return calls.stream().filter(call -> call.startsWith(method + " ")).count();
        }
    }

    private static FakeTransport fakeTransport(List<Object> items) {
        return new FakeTransport(items);
    }

    private static void check(boolean condition, String message) {
        if (!condition) {
            throw new AssertionError(message);
        }
        passed++;
    }
}
