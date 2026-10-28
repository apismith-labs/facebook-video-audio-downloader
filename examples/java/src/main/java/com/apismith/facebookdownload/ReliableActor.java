package com.apismith.facebookdownload;

import java.io.IOException;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * Staged runner for the Facebook Transcript Actor.
 *
 * <p>The three stages — create, poll, read Dataset — are separated because each
 * has a different retry rule. Run creation is attempted exactly once.
 */
public final class ReliableActor {
    public static final String APPROVED_BUILD_NUMBER = "0.0.18";
    public static final String APPROVED_BUILD_ID = "fZIlpYENof2jYqIJc";
    public static final String DEFAULT_ACTOR_ID = "apple_yang/facebook-transcript-scraper-api";
    public static final int DEFAULT_TIMEOUT_SECONDS = 900;
    public static final int DEFAULT_DATASET_LIMIT = 100;

    public static final Map<String, Integer> EXIT_CODES = Map.of(
            "success", 0,
            "actor_error", 4,
            "empty", 5,
            "no_transcript", 6,
            "unknown", 7,
            "transport_error", 8,
            "run_failed", 9,
            "creation_unknown", 10);

    private static final Pattern BEARER = Pattern.compile("(?i)(bearer\\s+)[A-Za-z0-9._~+/-]+=*");
    private static final Pattern SECRET = Pattern.compile(
            "(?i)(sessionid|token|authorization|cookie|password|api[_-]?key)(\\s*[:=]\\s*[\"']?)[^\\s,;&\"']+");

    private ReliableActor() {
    }

    /** A transport failure. Status 0 means no response arrived. */
    public static final class ApiException extends Exception {
        private static final long serialVersionUID = 1L;

        private final int status;
        private final double retryAfterSeconds;
        private final boolean ambiguous;

        public ApiException(String message, int status, double retryAfterSeconds, boolean ambiguous) {
            super(message);
            this.status = status;
            this.retryAfterSeconds = retryAfterSeconds;
            this.ambiguous = ambiguous;
        }

        public int status() {
            return status;
        }

        public double retryAfterSeconds() {
            return retryAfterSeconds;
        }

        public boolean ambiguous() {
            return ambiguous;
        }

        boolean retryable() {
            return status == 0 || status == 429 || (status >= 500 && status < 600);
        }
    }

    /** Injection seam for offline tests. */
    public interface Transport {
        Object request(String method, String path, Object body, Map<String, String> query) throws ApiException;
    }

    /** One HTTP request per call; retries belong to the staged runner. */
    public static final class HttpTransport implements Transport {
        private final String token;
        private final String baseUrl;
        private final HttpClient client;

        public HttpTransport(String token) {
            this(token, "https://api.apify.com/v2");
        }

        public HttpTransport(String token, String baseUrl) {
            this.token = token;
            this.baseUrl = baseUrl.endsWith("/") ? baseUrl.substring(0, baseUrl.length() - 1) : baseUrl;
            this.client = HttpClient.newBuilder()
                    .connectTimeout(Duration.ofSeconds(10))
                    .followRedirects(HttpClient.Redirect.NORMAL)
                    .build();
        }

        @Override
        public Object request(String method, String path, Object body, Map<String, String> query)
                throws ApiException {
            String target = baseUrl + path;
            if (query != null && !query.isEmpty()) {
                StringBuilder builder = new StringBuilder(target);
                builder.append(target.contains("?") ? '&' : '?');
                boolean first = true;
                for (Map.Entry<String, String> entry : query.entrySet()) {
                    if (!first) {
                        builder.append('&');
                    }
                    first = false;
                    builder.append(URLEncoder.encode(entry.getKey(), StandardCharsets.UTF_8));
                    builder.append('=');
                    builder.append(URLEncoder.encode(entry.getValue(), StandardCharsets.UTF_8));
                }
                target = builder.toString();
            }

            HttpRequest.Builder request = HttpRequest.newBuilder()
                    .uri(URI.create(target))
                    .timeout(Duration.ofSeconds(30))
                    .header("Authorization", "Bearer " + token)
                    .header("Content-Type", "application/json");
            if ("POST".equalsIgnoreCase(method)) {
                request.POST(HttpRequest.BodyPublishers.ofString(
                        body == null ? "{}" : Json.write(body), StandardCharsets.UTF_8));
            } else {
                request.GET();
            }

            HttpResponse<String> response;
            try {
                response = client.send(request.build(), HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8));
            } catch (IOException error) {
                throw new ApiException("Apify " + method.toUpperCase() + " transport failure: "
                        + error.getClass().getSimpleName(), 0, 0, "POST".equalsIgnoreCase(method));
            } catch (InterruptedException error) {
                Thread.currentThread().interrupt();
                throw new ApiException("Apify request was interrupted", 0, 0, "POST".equalsIgnoreCase(method));
            }

            if (response.statusCode() >= 400) {
                throw new ApiException("Apify HTTP " + response.statusCode(),
                        response.statusCode(),
                        parseRetryAfter(response.headers().firstValue("Retry-After").orElse("")),
                        "POST".equalsIgnoreCase(method) && response.statusCode() >= 500);
            }
            String text = response.body();
            if (text == null || text.isBlank()) {
                return new LinkedHashMap<String, Object>();
            }
            try {
                return Json.parse(text);
            } catch (RuntimeException error) {
                throw new ApiException("Apify returned invalid JSON", response.statusCode(), 0,
                        "POST".equalsIgnoreCase(method));
            }
        }
    }

    private static double parseRetryAfter(String value) {
        if (value == null || value.isBlank()) {
            return 0;
        }
        try {
            double seconds = Double.parseDouble(value.trim());
            return seconds < 0 || Double.isNaN(seconds) || Double.isInfinite(seconds) ? 0 : seconds;
        } catch (NumberFormatException ignored) {
            // Fall through to HTTP-date parsing.
        }
        try {
            Instant when = java.time.ZonedDateTime
                    .parse(value, java.time.format.DateTimeFormatter.RFC_1123_DATE_TIME).toInstant();
            double seconds = Duration.between(Instant.now(), when).toMillis() / 1000.0;
            return seconds < 0 ? 0 : seconds;
        } catch (RuntimeException ignored) {
            return 0;
        }
    }

    public static String redact(String value) {
        String redacted = BEARER.matcher(value).replaceAll("$1[REDACTED]");
        return SECRET.matcher(redacted).replaceAll("$1$2[REDACTED]");
    }

    public static String safeError(Throwable error) {
        if (error == null) {
            return "";
        }
        String message = error.getMessage() == null ? error.getClass().getSimpleName() : error.getMessage();
        String clean = redact(message.replace('\n', ' '));
        return clean.length() > 300 ? clean.substring(0, 300) : clean;
    }

    /** Append-only JSONL journal. */
    public interface Journal {
        void append(Map<String, Object> record) throws IOException;
    }

    public static final class MemoryJournal implements Journal {
        public final List<Map<String, Object>> records = new ArrayList<>();

        @Override
        public void append(Map<String, Object> record) {
            records.add(new LinkedHashMap<>(record));
        }
    }

    public static final class FileJournal implements Journal {
        private final Path path;

        public FileJournal(Path path) {
            this.path = path;
        }

        public Path path() {
            return path;
        }

        @Override
        public synchronized void append(Map<String, Object> record) throws IOException {
            validate();
            if (path.getParent() != null) {
                Files.createDirectories(path.getParent());
            }
            Files.writeString(path, Json.write(record) + "\n", StandardCharsets.UTF_8,
                    StandardOpenOption.CREATE, StandardOpenOption.WRITE, StandardOpenOption.APPEND);
            restrictPermissions(path);
        }

        public synchronized void validate() throws IOException {
            if (!Files.exists(path)) {
                return;
            }
            List<String> lines = Files.readAllLines(path, StandardCharsets.UTF_8);
            for (int index = 0; index < lines.size(); index++) {
                String line = lines.get(index).trim();
                if (line.isEmpty()) {
                    continue;
                }
                try {
                    Object parsed = Json.parse(line);
                    if (!(parsed instanceof Map)) {
                        throw new IOException("Run journal record at line " + (index + 1)
                                + " is not an object; refusing to continue");
                    }
                } catch (RuntimeException error) {
                    throw new IOException("Run journal is corrupt at line " + (index + 1)
                            + "; refusing to continue", error);
                }
            }
        }

        public synchronized Map<String, Object> latest(String runId) throws IOException {
            if (!Files.exists(path)) {
                return null;
            }
            Map<String, Object> found = null;
            for (String line : Files.readAllLines(path, StandardCharsets.UTF_8)) {
                if (line.trim().isEmpty()) {
                    continue;
                }
                Map<String, Object> record;
                try {
                    record = Json.asObject(Json.parse(line));
                } catch (RuntimeException error) {
                    throw new IOException("Run journal is corrupt; refusing resume", error);
                }
                if (runId.equals(record.get("runId"))) {
                    found = record;
                }
            }
            return found;
        }
    }

    private static void restrictPermissions(Path path) {
        try {
            Files.setPosixFilePermissions(path, PosixFilePermissions.fromString("rw-------"));
        } catch (UnsupportedOperationException | IOException ignored) {
            // Windows and other non-POSIX file systems.
        }
    }

    /** Classification result: status plus a human-readable message. */
    public record Classification(String status, String message) {
    }

    /** Applies the documented precedence: empty, errMsg, text, no_transcript, unknown. */
    public static Classification classifyItems(List<Object> items) {
        if (items == null) {
            return new Classification("unknown", "Dataset response is not an item list");
        }
        if (items.isEmpty()) {
            return new Classification("empty", "Dataset contains no items");
        }

        List<Map<String, Object>> objects = new ArrayList<>();
        for (Object item : items) {
            if (!(item instanceof Map)) {
                return new Classification("unknown", "Dataset contains a non-object item");
            }
            objects.add(Json.asObject(item));
        }

        List<String> messages = new ArrayList<>();
        for (Map<String, Object> item : objects) {
            Object errMsg = item.get("errMsg");
            if (errMsg != null && !String.valueOf(errMsg).isBlank()) {
                messages.add(String.valueOf(errMsg).trim());
            }
        }
        if (!messages.isEmpty()) {
            String joined = redact(String.join("; ", messages));
            return new Classification("actor_error", joined.length() > 500 ? joined.substring(0, 500) : joined);
        }

        for (Map<String, Object> item : objects) {
            if (item.get("text") instanceof String text && !text.isBlank()) {
                return new Classification("success", "");
            }
        }
        for (Map<String, Object> item : objects) {
            if (item.containsKey("text") && item.get("text") == null) {
                return new Classification("no_transcript", "Transcript text is null");
            }
        }
        for (Map<String, Object> item : objects) {
            if (!item.containsKey("text")) {
                return new Classification("no_transcript", "Transcript text is absent");
            }
        }
        for (Map<String, Object> item : objects) {
            if (item.get("text") instanceof String text && text.isBlank()) {
                return new Classification("no_transcript", "Transcript text is empty");
            }
        }
        return new Classification("unknown", "Transcript text has an unsupported type");
    }

    /** Keeps only well-formed segments; raw items stay untouched. */
    public static List<Map<String, Object>> normalizeSegments(Object value) {
        List<Map<String, Object>> result = new ArrayList<>();
        List<Object> list = Json.asList(value);
        if (list == null) {
            return result;
        }
        for (Object entry : list) {
            if (!(entry instanceof Map)) {
                continue;
            }
            Map<String, Object> object = Json.asObject(entry);
            Double start = asDouble(object.get("start"));
            Double end = asDouble(object.get("end"));
            Object text = object.get("text");
            if (start == null || end == null || !(text instanceof String)) {
                continue;
            }
            if (start.isNaN() || end.isNaN() || start.isInfinite() || end.isInfinite()) {
                continue;
            }
            if (start < 0 || end < start) {
                continue;
            }
            Map<String, Object> segment = new LinkedHashMap<>();
            segment.put("start", start);
            segment.put("end", end);
            segment.put("text", text);
            result.add(segment);
        }
        return result;
    }

    private static Double asDouble(Object value) {
        if (value instanceof Number number) {
            return number.doubleValue();
        }
        return null;
    }

    /** Everything ExecuteInput needs; zero values use the documented defaults. */
    public static final class RunOptions {
        public Transport transport;
        public String actorId = DEFAULT_ACTOR_ID;
        public Object input;
        public String sourceUrl = "";
        public Journal journal = new MemoryJournal();
        public int timeoutSeconds = DEFAULT_TIMEOUT_SECONDS;
        public int maxAttempts = 4;
        public String runId;
        public String datasetId;
        public String rowId;
        public String maxTotalChargeUsd;
        public boolean requireChargeCap;
        public String build;
        public boolean requireBuild;
        public Integer datasetLimit = DEFAULT_DATASET_LIMIT;
    }

    /** Normalized result of one staged execution. */
    public record Outcome(
            String rowId,
            String sourceUrl,
            String runId,
            String datasetId,
            String status,
            String error,
            String stage,
            List<Object> rawItems,
            Boolean datasetComplete,
            String buildNumber,
            String buildId,
            String buildVerification) {
    }

    public static Outcome executeInput(RunOptions options) {
        long deadline = System.nanoTime() + options.timeoutSeconds * 1_000_000_000L;
        String rowId = options.rowId != null && !options.rowId.isBlank() ? options.rowId : java.util.UUID.randomUUID().toString();

        Map<String, Object> record = new LinkedHashMap<>();
        record.put("rowId", rowId);
        record.put("sourceUrl", options.sourceUrl);
        record.put("actorId", options.actorId);
        record.put("stage", "start");
        record.put("status", "unknown");
        record.put("updatedAt", Instant.now().toString());
        if (options.runId != null) {
            record.put("runId", options.runId);
        }
        if (options.datasetId != null) {
            record.put("datasetId", options.datasetId);
        }

        String runId = options.runId;
        String datasetId = options.datasetId;
        String buildNumber = "";
        String buildId = "";
        String buildVerification = "";

        try {
            if (runId == null) {
                save(options.journal, record, "creating", "unknown", null);
                Map<String, String> query = new LinkedHashMap<>();
                if (options.maxTotalChargeUsd != null || (options.build != null && !options.build.isBlank())) {
                    query.put("restartOnError", "false");
                }
                if (options.maxTotalChargeUsd != null) {
                    query.put("maxTotalChargeUsd", options.maxTotalChargeUsd);
                }
                if (options.build != null && !options.build.isBlank()) {
                    query.put("build", options.build);
                }

                Object created;
                try {
                    created = options.transport.request("POST",
                            "/acts/" + options.actorId.replace("/", "~") + "/runs", options.input, query);
                } catch (ApiException error) {
                    if (error.status() == 429) {
                        String message = "HTTP 429 on Run creation; acceptance semantics are unverified; POST was not retried";
                        save(options.journal, record, "creation_unknown", "creation_unknown",
                                new ApiException(message, 429, 0, true));
                        return new Outcome(rowId, options.sourceUrl, null, null, "creation_unknown",
                                message, "creation_unknown", List.of(), null, "", "", "");
                    }
                    if (error.ambiguous() || error.status() == 0) {
                        save(options.journal, record, "creation_unknown", "creation_unknown", error);
                        return new Outcome(rowId, options.sourceUrl, null, null, "creation_unknown",
                                safeError(error), "creation_unknown", List.of(), null, "", "", "");
                    }
                    save(options.journal, record, "create_failed", "transport_error", error);
                    return new Outcome(rowId, options.sourceUrl, null, null, "transport_error",
                            safeError(error), "create_failed", List.of(), null, "", "", "");
                }

                Map<String, Object> payload = unwrap(created);
                runId = payload.get("id") instanceof String value ? value : null;
                datasetId = payload.get("defaultDatasetId") instanceof String value ? value : null;
                if (runId == null || runId.isBlank()) {
                    save(options.journal, record, "creation_unknown", "creation_unknown",
                            new IllegalStateException("POST response did not include a run ID"));
                    return new Outcome(rowId, options.sourceUrl, null, null, "creation_unknown",
                            "POST response did not include a run ID", "creation_unknown", List.of(), null, "", "", "");
                }
                record.put("runId", runId);
                record.put("datasetId", datasetId);
                save(options.journal, record, "run_created", "unknown", null);

                if (options.build != null && !options.build.isBlank()) {
                    buildNumber = stringValue(payload.get("buildNumber"));
                    buildId = stringValue(payload.get("buildId"));
                    buildVerification = "response";
                    if (buildNumber.isEmpty() || buildId.isEmpty()) {
                        Object verified;
                        try {
                            verified = retryGet(options, deadline, "/actor-runs/" + runId);
                        } catch (ApiException error) {
                            save(options.journal, record, "build_mismatch", "transport_error",
                                    new IllegalStateException("Could not verify actual Build identity: " + safeError(error)));
                            return new Outcome(rowId, options.sourceUrl, runId, datasetId, "transport_error",
                                    "Could not verify actual Build identity", "build_mismatch", List.of(), null,
                                    buildNumber, buildId, "failed");
                        }
                        Map<String, Object> verifiedPayload = unwrap(verified);
                        if (buildNumber.isEmpty()) {
                            buildNumber = stringValue(verifiedPayload.get("buildNumber"));
                        }
                        if (buildId.isEmpty()) {
                            buildId = stringValue(verifiedPayload.get("buildId"));
                        }
                        buildVerification = "run_get";
                    }
                    if (buildMismatch(options.build, buildNumber, buildId)) {
                        String kind = mismatchKind(buildNumber, buildId);
                        record.put("buildNumber", buildNumber);
                        record.put("buildId", buildId);
                        record.put("buildVerification", kind);
                        save(options.journal, record, "build_mismatch", "transport_error",
                                new IllegalStateException("Actual Build identity is missing or does not match the requested Build"));
                        return new Outcome(rowId, options.sourceUrl, runId, datasetId, "transport_error",
                                "Actual Build identity is missing or does not match the requested Build",
                                "build_mismatch", List.of(), null, buildNumber, buildId, kind);
                    }
                    record.put("buildNumber", buildNumber);
                    record.put("buildId", buildId);
                    record.put("buildVerification", buildVerification);
                    save(options.journal, record, "build_verified", "unknown", null);
                }
            } else {
                save(options.journal, record, "resuming", "unknown", null);
            }

            while (true) {
                if (System.nanoTime() > deadline) {
                    save(options.journal, record, "recoverable", "transport_error",
                            new IllegalStateException("Overall run deadline exceeded"));
                    return new Outcome(rowId, options.sourceUrl, runId, datasetId, "transport_error",
                            "Overall run deadline exceeded", "recoverable", List.of(), null,
                            buildNumber, buildId, buildVerification);
                }
                Map<String, Object> run = unwrap(retryGet(options, deadline, "/actor-runs/" + runId));

                if (options.build != null && !options.build.isBlank() && !record.containsKey("buildVerification")) {
                    String actualNumber = stringValue(run.get("buildNumber"));
                    String actualId = stringValue(run.get("buildId"));
                    if (buildMismatch(options.build, actualNumber, actualId)) {
                        String kind = mismatchKind(actualNumber, actualId);
                        record.put("buildNumber", actualNumber);
                        record.put("buildId", actualId);
                        record.put("buildVerification", kind);
                        save(options.journal, record, "build_mismatch", "transport_error",
                                new IllegalStateException("Run Build identity is missing or does not match the requested Build"));
                        return new Outcome(rowId, options.sourceUrl, runId, datasetId, "transport_error",
                                "Run Build identity is missing or does not match the requested Build",
                                "build_mismatch", List.of(), null, actualNumber, actualId, kind);
                    }
                    buildNumber = actualNumber;
                    buildId = actualId;
                    buildVerification = "run_get";
                    record.put("buildNumber", buildNumber);
                    record.put("buildId", buildId);
                    record.put("buildVerification", buildVerification);
                    save(options.journal, record, "build_verified", "unknown", null);
                }

                String status = stringValue(run.get("status"));
                if (List.of("SUCCEEDED", "FAILED", "TIMED-OUT", "ABORTED").contains(status)) {
                    if (!"SUCCEEDED".equals(status)) {
                        save(options.journal, record, "run_finished", "run_failed",
                                new IllegalStateException("Run ended with " + status));
                        return new Outcome(rowId, options.sourceUrl, runId, datasetId, "run_failed",
                                "Run ended with " + status, "run_finished", List.of(), null,
                                buildNumber, buildId, buildVerification);
                    }
                    String runDatasetId = stringValue(run.get("defaultDatasetId"));
                    if (datasetId != null && !runDatasetId.isEmpty() && !datasetId.equals(runDatasetId)) {
                        save(options.journal, record, "recoverable", "transport_error",
                                new IllegalStateException("Journal Dataset ID does not match the Run response"));
                        return new Outcome(rowId, options.sourceUrl, runId, datasetId, "transport_error",
                                "Journal Dataset ID does not match the Run response", "recoverable", List.of(), null,
                                buildNumber, buildId, buildVerification);
                    }
                    if (datasetId == null && !runDatasetId.isEmpty()) {
                        datasetId = runDatasetId;
                    }
                    record.put("datasetId", datasetId == null ? "" : datasetId);
                    save(options.journal, record, "run_finished", "unknown", null);
                    break;
                }

                long remainingMillis = (deadline - System.nanoTime()) / 1_000_000L;
                sleep(Math.max(0, Math.min(2000, remainingMillis)));
            }

            if (datasetId == null || datasetId.isBlank()) {
                save(options.journal, record, "recoverable", "transport_error",
                        new IllegalStateException("Run succeeded without a dataset ID"));
                return new Outcome(rowId, options.sourceUrl, runId, null, "transport_error",
                        "Run succeeded without a dataset ID", "recoverable", List.of(), null,
                        buildNumber, buildId, buildVerification);
            }

            int limit = options.datasetLimit == null ? DEFAULT_DATASET_LIMIT : options.datasetLimit;
            if (limit <= 0) {
                return new Outcome(rowId, options.sourceUrl, runId, datasetId, "transport_error",
                        "Dataset page limit must be a positive integer", "recoverable", List.of(), null,
                        buildNumber, buildId, buildVerification);
            }

            String datasetPath = "/datasets/" + datasetId + "/items?limit=" + limit + "&offset=0";
            List<Object> items = Json.asList(retryGet(options, deadline, datasetPath));
            if (items == null) {
                items = List.of();
            }

            for (int attempt = 0; attempt < 2; attempt++) {
                if (!items.isEmpty()) {
                    break;
                }
                if (System.nanoTime() + (attempt + 1) * 1_000_000_000L > deadline) {
                    break;
                }
                sleep((attempt + 1) * 1000L);
                items = Json.asList(retryGet(options, deadline, datasetPath));
                if (items == null) {
                    items = List.of();
                }
            }

            boolean complete = items.size() < limit;
            Classification classification = classifyItems(items);
            if (!complete) {
                classification = new Classification("unknown",
                        "Dataset reached the " + limit + "-item page limit; completeness is unverified");
            }
            save(options.journal, record, "complete", classification.status(),
                    classification.message().isEmpty() ? null : new IllegalStateException(classification.message()));

            return new Outcome(rowId, options.sourceUrl, runId, datasetId, classification.status(),
                    classification.message(), "complete", items, complete,
                    buildNumber, buildId, buildVerification);
        } catch (ApiException error) {
            return new Outcome(rowId, options.sourceUrl, runId, datasetId, "transport_error",
                    safeError(error), "recoverable", List.of(), null,
                    buildNumber, buildId, buildVerification);
        } catch (IOException error) {
            return new Outcome(rowId, options.sourceUrl, runId, datasetId, "transport_error",
                    safeError(error), "journal_error", List.of(), null,
                    buildNumber, buildId, buildVerification);
        }
    }

    private static Object retryGet(RunOptions options, long deadline, String path) throws ApiException {
        ApiException last = null;
        for (int attempt = 0; attempt < options.maxAttempts; attempt++) {
            if (System.nanoTime() > deadline) {
                if (last != null) {
                    throw last;
                }
                throw new ApiException("Overall run deadline exceeded", 0, 0, false);
            }
            try {
                return options.transport.request("GET", path, null, Map.of());
            } catch (ApiException error) {
                last = error;
                if (!error.retryable() || attempt + 1 >= options.maxAttempts) {
                    throw error;
                }
                double delaySeconds = error.retryAfterSeconds() > 0
                        ? error.retryAfterSeconds()
                        : Math.min(Math.pow(2, attempt), 8);
                if (System.nanoTime() + (long) (delaySeconds * 1_000_000_000L) > deadline) {
                    throw error;
                }
                sleep((long) (delaySeconds * 1000));
            }
        }
        throw last;
    }

    private static void save(Journal journal, Map<String, Object> record, String stage, String status,
                             Throwable error) throws IOException {
        record.put("stage", stage);
        record.put("status", status);
        record.put("updatedAt", Instant.now().toString());
        if (error != null) {
            record.put("error", safeError(error));
        } else {
            record.remove("error");
        }
        journal.append(new LinkedHashMap<>(record));
    }

    private static Map<String, Object> unwrap(Object value) {
        if (!(value instanceof Map)) {
            return new LinkedHashMap<>();
        }
        Map<String, Object> object = Json.asObject(value);
        Object data = object.get("data");
        if (data instanceof Map) {
            return Json.asObject(data);
        }
        return object;
    }

    static boolean buildMismatch(String requested, String buildNumber, String buildId) {
        if (buildNumber == null || buildNumber.isEmpty() || buildId == null || buildId.isEmpty()) {
            return true;
        }
        if (!requested.equals(buildNumber)) {
            return true;
        }
        return APPROVED_BUILD_NUMBER.equals(requested) && !APPROVED_BUILD_ID.equals(buildId);
    }

    private static String mismatchKind(String buildNumber, String buildId) {
        boolean emptyNumber = buildNumber == null || buildNumber.isEmpty();
        boolean emptyId = buildId == null || buildId.isEmpty();
        return emptyNumber && emptyId ? "missing" : "mismatch";
    }

    static String stringValue(Object value) {
        if (value == null) {
            return "";
        }
        if (value instanceof String text) {
            return text;
        }
        if (value instanceof Double number) {
            return number == Math.rint(number) ? String.valueOf(number.longValue()) : String.valueOf(number);
        }
        return String.valueOf(value);
    }

    private static void sleep(long millis) {
        if (millis <= 0) {
            return;
        }
        try {
            Thread.sleep(Math.min(millis, 2000));
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
        }
    }
}
