package com.apismith.facebookdownload;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Command line entry point: download the video, audio, transcript, and metadata for one URL.
 *
 * <p>One invocation may create one billable Actor Run ($0.0055 for a video under one minute). The
 * Run resolves the page into signed videoUrl/audioUrl links; those are downloaded immediately
 * because the signature expires.
 *
 * <p>Exit codes: 0 downloaded, 2 bad configuration, 4 Actor item error, 5 empty Dataset, 7 unknown,
 * 8 transport failure, 9 Run failed, 10 creation unknown, 11 media download failed.
 */
public final class Download {

    private Download() {
    }

    private static final Set<String> BOOLEAN_FLAGS = Set.of(
            "no-video", "audio", "force", "keep-media-urls", "json", "require-charge-cap", "quiet", "help");

    public static void main(String[] args) throws InterruptedException {
        Map<String, String> flags = new LinkedHashMap<>();
        List<String> positional = new ArrayList<>();
        for (int index = 0; index < args.length; index++) {
            String argument = args[index];
            if (argument.startsWith("--")) {
                String key = argument.substring(2);
                if (BOOLEAN_FLAGS.contains(key)) {
                    flags.put(key, "true");
                } else if (index + 1 < args.length) {
                    flags.put(key, args[++index]);
                } else {
                    System.err.println("missing value for --" + key);
                    System.exit(2);
                }
            } else {
                positional.add(argument);
            }
        }

        if (flags.containsKey("help") || positional.size() != 1) {
            System.out.println("""
                    Usage: download [options] <facebook-video-url>

                      --out DIR              output base directory (default: downloads)
                      --name NAME            override the output folder name
                      --no-video             skip video.mp4
                      --audio                also download the audio track
                      --audio-format F       raw | m4a | mp3 (default: raw)
                      --force                re-download existing files
                      --keep-media-urls      keep signed URLs in metadata.json
                      --json                 print a JSON report
                      --report PATH          also write the JSON report to PATH
                      --resume RUN_ID        reuse an existing Run (GET only)
                      --dataset-limit N      maximum Dataset items to read (default 100)
                      --timeout SECONDS      overall Run deadline (default 900)
                      --build BUILD          Actor Build
                      --actor-id ID          override the Actor ID
                      --max-total-charge-usd X  per-Run charge cap
                      --require-charge-cap   fail before creating a Run without a cap
                      --quiet                suppress progress output
                    """);
            System.exit(flags.containsKey("help") ? 0 : 2);
        }

        String audioFormat = flags.getOrDefault("audio-format", "raw");
        if (!Set.of("raw", "m4a", "mp3").contains(audioFormat)) {
            System.err.println("--audio-format must be raw, m4a, or mp3");
            System.exit(2);
        }
        if (!"raw".equals(audioFormat) && !flags.containsKey("audio")) {
            System.err.println("--audio-format requires --audio");
            System.exit(2);
        }

        Map<String, String> dotEnv = TranscriptClient.readDotEnv(TranscriptClient.findDotEnv());
        TranscriptClient.Options run = new TranscriptClient.Options();
        run.token = firstNonBlank(flags.get("token"), dotEnv.get("APIFY_TOKEN"),
                System.getenv().getOrDefault("APIFY_TOKEN", ""));
        run.actorId = flags.getOrDefault("actor-id", dotEnv.getOrDefault("APIFY_ACTOR_ID",
                ReliableActor.DEFAULT_ACTOR_ID));
        run.build = flags.getOrDefault("build", ReliableActor.APPROVED_BUILD_NUMBER);
        run.runId = flags.get("resume");
        run.requireChargeCap = flags.containsKey("require-charge-cap")
                || "1".equals(dotEnv.getOrDefault("APIFY_REQUIRE_MAX_TOTAL_CHARGE_USD", "0"));
        run.maxTotalChargeUsd = firstNonBlank(flags.get("max-total-charge-usd"),
                dotEnv.get("APIFY_MAX_TOTAL_CHARGE_USD"),
                System.getenv().getOrDefault("APIFY_MAX_TOTAL_CHARGE_USD", ""));
        if (flags.containsKey("timeout")) {
            run.timeoutSeconds = Integer.parseInt(flags.get("timeout"));
        }
        if (flags.containsKey("dataset-limit")) {
            run.datasetLimit = Integer.parseInt(flags.get("dataset-limit"));
        }

        Downloader.Options options = new Downloader.Options();
        options.outDir = flags.getOrDefault("out", "downloads");
        options.name = flags.get("name");
        options.wantVideo = !flags.containsKey("no-video");
        options.wantAudio = flags.containsKey("audio");
        options.audioFormat = audioFormat;
        options.force = flags.containsKey("force");
        options.keepMediaUrls = flags.containsKey("keep-media-urls");
        options.run = run;
        options.progress = progress(flags.containsKey("quiet"));

        Downloader.Outcome outcome = Downloader.downloadOne(positional.get(0), options);
        Map<String, Object> report = outcome.report();
        boolean wantsJson = flags.containsKey("json");

        if (wantsJson) {
            System.out.println(Json.write(report));
        } else if (!flags.containsKey("quiet")) {
            if (outcome.exitCode() == 0) {
                StringBuilder files = new StringBuilder();
                Object assets = report.get("assets");
                if (assets instanceof List<?> list) {
                    for (Object entry : list) {
                        if (entry instanceof Map<?, ?> asset) {
                            if (files.length() > 0) {
                                files.append(", ");
                            }
                            files.append(Path.of(String.valueOf(asset.get("path"))).getFileName());
                        }
                    }
                }
                System.out.println(report.get("status") + ": " + report.get("outputDir")
                        + "  segments=" + report.get("segments")
                        + "  " + (files.length() == 0 ? "no media" : files));
            } else {
                System.err.println(report.get("status") + ": " + report.get("error"));
            }
        }

        String reportPath = flags.get("report");
        if (reportPath != null) {
            try {
                Path target = Path.of(reportPath);
                if (target.getParent() != null) {
                    Files.createDirectories(target.getParent());
                }
                Files.writeString(target, Json.write(report) + "\n", StandardCharsets.UTF_8);
            } catch (IOException error) {
                System.err.println("could not write report: " + error.getMessage());
            }
        }
        System.exit(outcome.exitCode());
    }

    private static String firstNonBlank(String... values) {
        for (String value : values) {
            if (value != null && !value.isBlank()) {
                return value;
            }
        }
        return "";
    }

    private static Media.Progress progress(boolean quiet) {
        if (quiet) {
            return (kind, written, total) -> {
            };
        }
        Map<String, Long> state = new LinkedHashMap<>();
        return (kind, written, total) -> {
            long last = state.getOrDefault(kind, 0L);
            if (written - last < 5L * 1024 * 1024 && !(total > 0 && written >= total)) {
                return;
            }
            state.put(kind, written);
            String share = total > 0 ? " (" + (written * 100 / total) + "%)" : "";
            System.err.print("  " + kind + ": " + humanBytes(written) + share + "\r");
        };
    }

    private static String humanBytes(long value) {
        if (value <= 0) {
            return "0 B";
        }
        String[] units = {"B", "KB", "MB", "GB", "TB"};
        double size = value;
        int unit = 0;
        while (size >= 1024 && unit < units.length - 1) {
            size /= 1024;
            unit++;
        }
        return unit == 0 ? value + " B" : String.format("%.1f %s", size, units[unit]);
    }
}
