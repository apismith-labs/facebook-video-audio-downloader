package com.apismith.facebookdownload;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * One-video download pipeline: Actor Run, media download, sidecar files, manifest.
 *
 * <p>Shared by the single-URL and batch CLIs so both produce the same folder layout and status
 * vocabulary as the Python, Node, and Go clients.
 */
public final class Downloader {

    public static final int DOWNLOAD_FAILED_EXIT = 11;
    private static final Set<String> DOWNLOADABLE = Set.of("success");
    private static final Map<String, Integer> RESOLVE_EXITS = Map.of(
            "actor_error", 4, "empty", 5, "unknown", 7, "run_failed", 9);

    private Downloader() {
    }

    /**
     * Seam for the ffmpeg call, mirroring Go's {@code TranscodeAudio} runner parameter. Production
     * code leaves it at the default so tests can assert the conversion policy without ffmpeg.
     */
    @FunctionalInterface
    public interface Transcoder {
        void run(Path source, Path target, String audioFormat)
                throws Media.MediaException, IOException, InterruptedException;
    }

    /** Download tuning plus the Actor Run settings. */
    public static final class Options {
        public String outDir = "downloads";
        public boolean wantVideo = true;
        public boolean wantAudio;
        public String audioFormat = "raw";
        public boolean force;
        public boolean keepMediaUrls;
        public String name;
        public VideoClient.Options run = new VideoClient.Options();
        public Media.Progress progress;
        public Transcoder transcoder = Media::transcodeAudio;
    }

    /** Machine-readable outcome plus the process exit code. */
    public record Outcome(Map<String, Object> report, int exitCode) {
    }

    private static Map<String, Object> emptyReport(String sourceUrl) {
        Map<String, Object> report = new LinkedHashMap<>();
        report.put("status", "unknown");
        report.put("sourceUrl", sourceUrl);
        report.put("canonicalUrl", null);
        report.put("urlForm", null);
        report.put("runId", null);
        report.put("datasetId", null);
        report.put("outputDir", null);
        report.put("duration", null);
        report.put("title", null);
        report.put("nickname", null);
        report.put("assets", new ArrayList<>());
        report.put("error", null);
        return report;
    }

    /** Resolves one URL, downloads the requested media, and writes the sidecar files. */
    public static Outcome downloadOne(String url, Options options) throws InterruptedException {
        Options settings = options == null ? new Options() : options;
        Map<String, Object> report = emptyReport(url);

        VideoClient.Result result;
        try {
            result = VideoClient.resolveMedia(url, settings.run);
        } catch (VideoClient.ConfigurationException error) {
            report.put("status", "config_error");
            report.put("error", error.getMessage());
            return new Outcome(report, 2);
        } catch (VideoClient.CreationUnknownException error) {
            report.put("status", "creation_unknown");
            report.put("error", error.getMessage());
            return new Outcome(report, 10);
        } catch (VideoClient.ProtocolException error) {
            report.put("status", "unknown");
            report.put("runId", error.runId());
            report.put("datasetId", error.datasetId());
            report.put("error", error.getMessage());
            return new Outcome(report, 7);
        } catch (VideoClient.TransportException error) {
            report.put("status", "transport_error");
            report.put("runId", error.runId());
            report.put("datasetId", error.datasetId());
            report.put("error", error.getMessage());
            return new Outcome(report, 8);
        }

        report.put("status", result.status());
        report.put("canonicalUrl", result.canonicalUrl());
        report.put("urlForm", result.urlForm());
        report.put("runId", result.runId());
        report.put("datasetId", result.datasetId());
        report.put("duration", result.duration());
        report.put("title", result.title());
        report.put("nickname", result.nickname());
        report.put("error", result.error());

        Map<String, Object> item = firstItem(result.items());
        String folder = settings.name != null && !settings.name.isBlank()
                ? settings.name
                : Layout.folderName(result.canonicalUrl(), result.urlForm(), result.title());
        Path target = Layout.chooseOutputDir(Path.of(settings.outDir), folder, result.canonicalUrl());
        report.put("outputDir", target.toString());

        try {
            if (!DOWNLOADABLE.contains(result.status())) {
                Layout.writeJson(target.resolve("metadata.json"),
                        Layout.sanitizeItem(item, settings.keepMediaUrls));
                return new Outcome(report, RESOLVE_EXITS.getOrDefault(result.status(), 7));
            }

            Layout.writeJson(target.resolve("metadata.json"),
                    Layout.sanitizeItem(item, settings.keepMediaUrls));

            List<Map<String, Object>> mediaSources = new ArrayList<>();
            List<Map<String, Object>> assets = new ArrayList<>();
            String failure = null;

            List<Map.Entry<String, String>> jobs = new ArrayList<>();
            Object videoUrl = item.get("videoUrl");
            Object audioUrl = item.get("audioUrl");
            if (settings.wantVideo && videoUrl instanceof String text && !text.isEmpty()) {
                jobs.add(Map.entry("video", text));
            }
            if (settings.wantAudio && audioUrl instanceof String text && !text.isEmpty()) {
                jobs.add(Map.entry("audio", text));
            }

            if (jobs.isEmpty()) {
                failure = "the Dataset item carries no media URL for the requested output ("
                        + result.status() + ")";
                report.put("error", failure);
            }

            for (Map.Entry<String, String> job : jobs) {
                String kind = job.getKey();
                mediaSources.add(sourceRecord(kind, job.getValue()));
                Path destination = target.resolve(kind + ".mp4");
                try {
                    Media.Options mediaOptions = new Media.Options();
                    mediaOptions.kind = kind;
                    mediaOptions.force = settings.force;
                    mediaOptions.progress = settings.progress;
                    Media.Asset asset = Media.download(job.getValue(), destination, mediaOptions);
                    assets.add(asset.toMap());

                    if ("audio".equals(kind) && !"raw".equals(settings.audioFormat)) {
                        Path converted = Media.audioTarget(destination, settings.audioFormat);
                        // --force re-derives the converted container too; otherwise a re-downloaded
                        // audio.mp4 would sit next to a stale audio.m4a.
                        boolean rebuilt = settings.force || !Files.isRegularFile(converted);
                        if (rebuilt) {
                            settings.transcoder.run(destination, converted, settings.audioFormat);
                        }
                        Map<String, Object> extra = new LinkedHashMap<>();
                        extra.put("kind", "audio-converted");
                        extra.put("path", converted.toString());
                        extra.put("bytes", Files.size(converted));
                        extra.put("sha256", "");
                        extra.put("contentType", null);
                        extra.put("sourceHost", "");
                        extra.put("skipped", !rebuilt);
                        assets.add(extra);
                    }
                } catch (Media.MediaException error) {
                    failure = kind + " download failed: " + error.getMessage();
                    report.put("error", failure);
                    break;
                } catch (IOException error) {
                    failure = kind + " download failed: " + error.getMessage();
                    report.put("error", failure);
                    break;
                }
            }

            try {
                Map<String, Object> manifest = Layout.buildManifest(result, assets, mediaSources);
                Layout.writeJson(target.resolve("manifest.json"), manifest);
            } catch (IOException error) {
                report.put("error", "could not write manifest: " + error.getMessage());
                return new Outcome(report, DOWNLOAD_FAILED_EXIT);
            }
            report.put("assets", assets);
            return new Outcome(report, failure == null ? 0 : DOWNLOAD_FAILED_EXIT);
        } catch (IOException error) {
            report.put("error", "could not write output files: " + error.getMessage());
            return new Outcome(report, DOWNLOAD_FAILED_EXIT);
        }
    }

    private static Map<String, Object> sourceRecord(String kind, String url) {
        Map<String, Object> record = new LinkedHashMap<>();
        record.put("kind", kind);
        record.put("url", Media.redactMediaUrl(url));
        return record;
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> firstItem(List<Object> items) {
        if (items == null || items.isEmpty()) {
            return new LinkedHashMap<>();
        }
        Object first = items.get(0);
        if (first instanceof Map<?, ?> map) {
            return (Map<String, Object>) map;
        }
        return new LinkedHashMap<>();
    }
}
