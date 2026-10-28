package com.apismith.facebookdownload;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;

/**
 * Creates one Run per CSV row. Plan-only by default: without --execute it
 * validates every row, prints a cost estimate, reads no token, creates no Run.
 */
public final class BatchDownload {
    private static final DateTimeFormatter STAMP =
            DateTimeFormatter.ofPattern("yyyyMMdd'T'HHmmss'Z'").withZone(ZoneOffset.UTC);

    private BatchDownload() {
    }

    private record Row(int line, String url, Double seconds) {
    }

    public static void main(String[] args) {
        Map<String, String> flags = new LinkedHashMap<>();
        List<String> positional = new ArrayList<>();
        for (int index = 0; index < args.length; index++) {
            String argument = args[index];
            if (argument.startsWith("--")) {
                String key = argument.substring(2);
                if (List.of("execute", "require-charge-cap", "no-video", "audio", "force").contains(key)) {
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
        if (positional.size() != 1) {
            System.err.println("usage: batch-download [--execute] [--out DIR] [--limit N] [--max-concurrency N] <file.csv>");
            System.exit(2);
        }

        Path csvPath = Path.of(positional.get(0));
        List<Row> rows;
        try {
            rows = readCsv(csvPath);
        } catch (IOException | IllegalArgumentException error) {
            System.err.println(error.getMessage());
            System.exit(2);
            return;
        }

        int limit = Integer.parseInt(flags.getOrDefault("limit", "0"));
        if (limit > 0 && limit < rows.size()) {
            rows = new ArrayList<>(rows.subList(0, limit));
        }
        if (rows.isEmpty()) {
            System.err.println("no data rows found");
            System.exit(2);
        }

        String tier = flags.getOrDefault("tier", "FREE").toUpperCase();
        double estimate = 0;
        List<String[]> canonical = new ArrayList<>();
        for (Row row : rows) {
            try {
                String[] validated = TranscriptClient.validateVideoUrl(row.url());
                canonical.add(validated);
                estimate += Cost.estimate(1, row.seconds() == null ? 60 : row.seconds(), tier);
            } catch (TranscriptClient.ConfigurationException error) {
                System.err.println("line " + row.line() + ": " + error.getMessage());
                System.exit(2);
            }
        }

        System.out.println("rows: " + rows.size());
        System.out.println("tier: " + tier);
        System.out.println("estimated cost: " + Cost.formatUsd(estimate));
        if (!"true".equals(flags.get("execute"))) {
            System.out.println("plan only: no token read, no Run created. Re-run with --execute to submit.");
            return;
        }

        Map<String, String> dotEnv = TranscriptClient.readDotEnv(TranscriptClient.findDotEnv());
        String token = dotEnv.getOrDefault("APIFY_TOKEN", System.getenv().getOrDefault("APIFY_TOKEN", ""));
        if (token.isBlank()) {
            System.err.println("APIFY_TOKEN is required with --execute");
            System.exit(2);
        }
        String cap = flags.getOrDefault("max-total-charge-usd",
                dotEnv.getOrDefault("APIFY_MAX_TOTAL_CHARGE_USD",
                        System.getenv().getOrDefault("APIFY_MAX_TOTAL_CHARGE_USD", "")));
        if ("true".equals(flags.get("require-charge-cap")) && cap.isBlank()) {
            System.err.println("APIFY_MAX_TOTAL_CHARGE_USD is required with --require-charge-cap");
            System.exit(2);
        }

        int concurrency = Math.max(1, Integer.parseInt(flags.getOrDefault("max-concurrency", "3")));
        int timeout = Integer.parseInt(flags.getOrDefault("timeout",
                String.valueOf(ReliableActor.DEFAULT_TIMEOUT_SECONDS)));
        ReliableActor.FileJournal journal = new ReliableActor.FileJournal(
                csvPath.toAbsolutePath().getParent().resolve("java-run-journal.jsonl"));

        Downloader.Options downloadOptions = new Downloader.Options();
        downloadOptions.outDir = flags.getOrDefault("out", "downloads");
        downloadOptions.wantVideo = !flags.containsKey("no-video");
        downloadOptions.wantAudio = flags.containsKey("audio");
        downloadOptions.audioFormat = flags.getOrDefault("audio-format", "raw");
        downloadOptions.force = flags.containsKey("force");
        TranscriptClient.Options sharedRun = new TranscriptClient.Options();
        sharedRun.token = token;
        sharedRun.maxTotalChargeUsd = cap;
        sharedRun.timeoutSeconds = timeout;
        downloadOptions.run = sharedRun;

        Object[] results = new Object[rows.size()];
        final List<Row> workRows = rows;

        ExecutorService pool = Executors.newFixedThreadPool(concurrency);
        List<Future<?>> futures = new ArrayList<>();
        for (int index = 0; index < workRows.size(); index++) {
            final int position = index;
            futures.add(pool.submit(() -> {
                results[position] = executeRow(workRows.get(position), downloadOptions, journal);
            }));
        }
        for (Future<?> future : futures) {
            try {
                future.get();
            } catch (Exception error) {
                System.err.println("row execution failed: " + error.getMessage());
            }
        }
        pool.shutdown();

        Map<String, Object> summary = new LinkedHashMap<>();
        int failed = 0;
        for (Object item : results) {
            Map<String, Object> result = item instanceof Map<?, ?> map
                    ? Json.asObject(map)
                    : Map.of("status", "unknown");
            String status = String.valueOf(result.get("status"));
            summary.merge(status, 1, (left, right) -> ((Number) left).intValue() + ((Number) right).intValue());
            if (result.get("exitCode") instanceof Number code && code.intValue() != 0) {
                failed++;
            } else if (!"success".equals(status) && !"no_transcript".equals(status)) {
                failed++;
            }
        }
        summary.put("processed", results.length);
        summary.put("failed", failed);

        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("generatedAt", Instant.now().toString());
        payload.put("csv", csvPath.toString());
        payload.put("tier", tier);
        payload.put("outputDir", Path.of(downloadOptions.outDir).toAbsolutePath().toString());
        payload.put("estimateUsd", estimate);
        payload.put("summary", summary);
        payload.put("rows", results);

        String output = flags.get("output");
        Path target = output != null ? Path.of(output) : uniqueResultPath(csvPath.toAbsolutePath().getParent());
        try {
            if (target.getParent() != null) {
                Files.createDirectories(target.getParent());
            }
            Files.writeString(target, Json.write(payload) + "\n", StandardCharsets.UTF_8);
        } catch (IOException error) {
            System.err.println("could not write " + target + ": " + error.getMessage());
            System.exit(2);
        }

        System.out.println("results: " + target);
        System.out.println("success: " + summary.getOrDefault("success", 0));
        System.out.println("failed: " + failed);
        if (failed > 0) {
            System.exit(1);
        }
    }

    private static Map<String, Object> executeRow(Row row, Downloader.Options template,
                                                  ReliableActor.Journal journal) {
        Downloader.Options options = new Downloader.Options();
        options.outDir = template.outDir;
        options.wantVideo = template.wantVideo;
        options.wantAudio = template.wantAudio;
        options.audioFormat = template.audioFormat;
        options.force = template.force;
        options.keepMediaUrls = template.keepMediaUrls;

        TranscriptClient.Options run = new TranscriptClient.Options();
        run.token = template.run.token;
        run.actorId = template.run.actorId;
        run.build = template.run.build;
        run.maxTotalChargeUsd = template.run.maxTotalChargeUsd;
        run.requireChargeCap = template.run.requireChargeCap;
        run.timeoutSeconds = template.run.timeoutSeconds;
        run.datasetLimit = template.run.datasetLimit;
        run.rowId = String.valueOf(row.line());
        run.journal = journal;
        options.run = run;

        Map<String, Object> entry;
        try {
            Downloader.Outcome outcome = Downloader.downloadOne(row.url(), options);
            entry = new LinkedHashMap<>(outcome.report());
            entry.put("exitCode", outcome.exitCode());
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
            entry = new LinkedHashMap<>();
            entry.put("status", "unknown");
            entry.put("error", "interrupted");
            entry.put("exitCode", 7);
        }
        entry.put("rowId", String.valueOf(row.line()));
        entry.putIfAbsent("sourceUrl", row.url());
        return entry;
    }

    private static List<Row> readCsv(Path path) throws IOException {
        List<String> lines = Files.readAllLines(path, StandardCharsets.UTF_8);
        if (lines.isEmpty()) {
            throw new IllegalArgumentException("CSV is empty");
        }
        List<String> header = splitLine(lines.get(0).replace("\uFEFF", ""));
        int urlIndex = -1;
        int durationIndex = -1;
        for (int index = 0; index < header.size(); index++) {
            String name = header.get(index).trim().toLowerCase();
            if ("videourl".equals(name) || "url".equals(name)) {
                urlIndex = index;
            } else if ("durationseconds".equals(name) || "duration".equals(name)) {
                durationIndex = index;
            }
        }
        if (urlIndex < 0) {
            throw new IllegalArgumentException("CSV header must contain videoUrl or url");
        }

        List<Row> rows = new ArrayList<>();
        for (int index = 1; index < lines.size(); index++) {
            if (lines.get(index).isBlank()) {
                continue;
            }
            List<String> fields = splitLine(lines.get(index));
            if (urlIndex >= fields.size()) {
                continue;
            }
            String url = fields.get(urlIndex).trim();
            if (url.isEmpty()) {
                continue;
            }
            Double seconds = null;
            if (durationIndex >= 0 && durationIndex < fields.size() && !fields.get(durationIndex).isBlank()) {
                try {
                    double parsed = Double.parseDouble(fields.get(durationIndex).trim());
                    if (parsed >= 0) {
                        seconds = parsed;
                    }
                } catch (NumberFormatException ignored) {
                    // An unparsable duration only weakens the estimate.
                }
            }
            rows.add(new Row(index + 1, url, seconds));
        }
        return rows;
    }

    private static List<String> splitLine(String line) {
        List<String> fields = new ArrayList<>();
        StringBuilder current = new StringBuilder();
        boolean quoted = false;
        for (int index = 0; index < line.length(); index++) {
            char character = line.charAt(index);
            if (character == '"') {
                if (quoted && index + 1 < line.length() && line.charAt(index + 1) == '"') {
                    current.append('"');
                    index++;
                } else {
                    quoted = !quoted;
                }
            } else if (character == ',' && !quoted) {
                fields.add(current.toString());
                current.setLength(0);
            } else {
                current.append(character);
            }
        }
        fields.add(current.toString());
        return fields;
    }

    private static Path uniqueResultPath(Path directory) {
        String base = "facebook-batch-results-" + STAMP.format(Instant.now());
        Path candidate = directory.resolve(base + ".json");
        int suffix = 1;
        while (Files.exists(candidate)) {
            candidate = directory.resolve(base + "-" + suffix + ".json");
            suffix++;
        }
        return candidate;
    }
}
