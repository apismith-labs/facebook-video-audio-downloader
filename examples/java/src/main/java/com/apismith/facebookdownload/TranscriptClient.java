package com.apismith.facebookdownload;

import java.io.IOException;
import java.net.URI;
import java.net.URISyntaxException;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** URL validation plus the reusable {@code getTranscript} entry point. */
public final class TranscriptClient {
    private static final int DEFAULT_DATASET_LIMIT = ReliableActor.DEFAULT_DATASET_LIMIT;

    private static final Set<String> ALLOWED_HOSTS = Set.of(
            "facebook.com", "www.facebook.com", "m.facebook.com",
            "web.facebook.com", "fb.watch", "www.fb.watch");

    private static final Set<String> RESERVED_SEGMENTS = Set.of(
            "about", "business", "events", "fundraisers", "gaming", "groups", "help", "login",
            "login.php", "marketplace", "messages", "permalink.php", "photo", "plugins",
            "policies", "settings", "sharer", "stories", "story.php", "watchparty");

    private static final Pattern WATCH = Pattern.compile("^/watch/?$", Pattern.CASE_INSENSITIVE);
    private static final Pattern VIDEO_PHP = Pattern.compile("^/video\\.php$", Pattern.CASE_INSENSITIVE);
    private static final Pattern PAGE_VIDEO = Pattern.compile(
            "^/([A-Za-z0-9._-]+)/videos/(\\d+)/?$", Pattern.CASE_INSENSITIVE);
    private static final Pattern VIDEOS = Pattern.compile("^/videos/(\\d+)/?$", Pattern.CASE_INSENSITIVE);
    private static final Pattern REEL = Pattern.compile("^/reel/(\\d+)/?$", Pattern.CASE_INSENSITIVE);
    private static final Pattern SHARE = Pattern.compile(
            "^/share/([vr])/([A-Za-z0-9_-]+)/?$", Pattern.CASE_INSENSITIVE);
    private static final Pattern SHORT = Pattern.compile("^/([A-Za-z0-9_-]+)/?$");
    private static final Pattern DIGITS = Pattern.compile("^\\d+$");
    private static final Pattern BUILD = Pattern.compile("^\\d+(?:\\.\\d+){0,2}$");

    private TranscriptClient() {
    }

    public static final class ConfigurationException extends Exception {
        private static final long serialVersionUID = 1L;

        public ConfigurationException(String message) {
            super(message);
        }
    }

    public static final class ProtocolException extends Exception {
        private static final long serialVersionUID = 1L;

        private final String runId;
        private final String datasetId;

        public ProtocolException(String message, String runId, String datasetId) {
            super(message);
            this.runId = runId;
            this.datasetId = datasetId;
        }

        public String runId() {
            return runId;
        }

        public String datasetId() {
            return datasetId;
        }
    }

    public static final class CreationUnknownException extends Exception {
        private static final long serialVersionUID = 1L;

        public CreationUnknownException(String message) {
            super(message);
        }
    }

    public static final class TransportException extends Exception {
        private static final long serialVersionUID = 1L;

        private final String runId;
        private final String datasetId;

        public TransportException(String message, String runId, String datasetId) {
            super(message);
            this.runId = runId;
            this.datasetId = datasetId;
        }

        public String runId() {
            return runId;
        }

        public String datasetId() {
            return datasetId;
        }
    }

    /** Outcome of one Actor Run. Items keeps the Actor's original fields. */
    public record Result(
            String status,
            String text,
            List<Map<String, Object>> segments,
            List<Object> items,
            String runId,
            String datasetId,
            String sourceUrl,
            String canonicalUrl,
            String urlForm,
            String error,
            boolean datasetComplete,
            String title,
            String nickname,
            Double duration) {

        public boolean succeeded() {
            return "success".equals(status);
        }

        public int exitCode() {
            return ReliableActor.EXIT_CODES.getOrDefault(status, 3);
        }
    }

    /** Call configuration. Zero values fall back to environment and defaults. */
    public static final class Options {
        public String token;
        public String actorId = ReliableActor.DEFAULT_ACTOR_ID;
        public String build = ReliableActor.APPROVED_BUILD_NUMBER;
        public int timeoutSeconds = ReliableActor.DEFAULT_TIMEOUT_SECONDS;
        public String maxTotalChargeUsd;
        public boolean requireChargeCap;
        public int datasetLimit = DEFAULT_DATASET_LIMIT;
        public String runId;
        public String rowId;
        public ReliableActor.Transport transport;
        public ReliableActor.Journal journal;
    }

    /** Returns {canonicalUrl, form}. */
    public static String[] validateVideoUrl(String raw) throws ConfigurationException {
        if (raw == null || raw.isEmpty() || !raw.equals(raw.trim())) {
            throw new ConfigurationException("url must be a bare Facebook video URL without surrounding whitespace");
        }
        if (raw.length() > 2048) {
            throw new ConfigurationException("url is longer than 2048 characters");
        }
        if (raw.toLowerCase(Locale.ROOT).startsWith("http://")) {
            throw new ConfigurationException("url must use https://; http links are rejected");
        }

        URI parsed;
        try {
            parsed = new URI(raw);
        } catch (URISyntaxException error) {
            throw new ConfigurationException("url could not be parsed");
        }
        if (!"https".equals(parsed.getScheme())) {
            throw new ConfigurationException("url must be an absolute https:// Facebook video URL");
        }
        if (parsed.getUserInfo() != null) {
            throw new ConfigurationException("url must not contain user information");
        }
        if (parsed.getPort() != -1 && parsed.getPort() != 443) {
            throw new ConfigurationException("url must not use a non-default port");
        }
        String host = parsed.getHost() == null ? "" : parsed.getHost().toLowerCase(Locale.ROOT);
        if (host.endsWith(".")) {
            host = host.substring(0, host.length() - 1);
        }
        if (!ALLOWED_HOSTS.contains(host)) {
            throw new ConfigurationException(
                    "url host must be facebook.com, www.facebook.com, m.facebook.com, web.facebook.com, or fb.watch");
        }
        if (parsed.getFragment() != null) {
            throw new ConfigurationException("url must not contain a fragment");
        }

        String path = parsed.getPath() == null || parsed.getPath().isEmpty() ? "/" : parsed.getPath();
        Map<String, List<String>> query = parseQuery(parsed.getRawQuery());

        if ("fb.watch".equals(host) || "www.fb.watch".equals(host)) {
            Matcher matcher = SHORT.matcher(path);
            if (!matcher.matches()) {
                throw new ConfigurationException("fb.watch URLs must look like https://fb.watch/<code>/");
            }
            return new String[] {"https://fb.watch/" + matcher.group(1) + "/", "short_link"};
        }

        if (WATCH.matcher(path).matches()) {
            String id = singleV(query);
            if (id == null) {
                throw new ConfigurationException("watch URLs must include exactly one numeric ?v= parameter");
            }
            return new String[] {"https://www.facebook.com/watch/?v=" + id, "watch"};
        }

        if (VIDEO_PHP.matcher(path).matches()) {
            String id = singleV(query);
            if (id == null) {
                throw new ConfigurationException("video.php URLs must include exactly one numeric ?v= parameter");
            }
            return new String[] {"https://www.facebook.com/video.php?v=" + id, "video_php"};
        }

        Matcher pageVideo = PAGE_VIDEO.matcher(path);
        if (pageVideo.matches()) {
            if (RESERVED_SEGMENTS.contains(pageVideo.group(1).toLowerCase(Locale.ROOT))) {
                throw new ConfigurationException("url points at a site section, not a video");
            }
            return new String[] {
                    "https://www.facebook.com/" + pageVideo.group(1) + "/videos/" + pageVideo.group(2) + "/",
                    "page_video"};
        }

        Matcher videos = VIDEOS.matcher(path);
        if (videos.matches()) {
            return new String[] {"https://www.facebook.com/videos/" + videos.group(1) + "/", "video"};
        }

        Matcher reel = REEL.matcher(path);
        if (reel.matches()) {
            return new String[] {"https://www.facebook.com/reel/" + reel.group(1) + "/", "reel"};
        }

        Matcher share = SHARE.matcher(path);
        if (share.matches()) {
            return new String[] {
                    "https://www.facebook.com/share/" + share.group(1).toLowerCase(Locale.ROOT) + "/"
                            + share.group(2) + "/",
                    "share"};
        }

        throw new ConfigurationException(
                "unsupported Facebook URL form; see docs/input-output-fields.md#accepted-url-forms");
    }

    private static Map<String, List<String>> parseQuery(String rawQuery) {
        Map<String, List<String>> values = new LinkedHashMap<>();
        if (rawQuery == null || rawQuery.isEmpty()) {
            return values;
        }
        for (String pair : rawQuery.split("&")) {
            if (pair.isEmpty()) {
                continue;
            }
            int separator = pair.indexOf('=');
            String key = separator < 0 ? pair : pair.substring(0, separator);
            String value = separator < 0 ? "" : pair.substring(separator + 1);
            values.computeIfAbsent(java.net.URLDecoder.decode(key, java.nio.charset.StandardCharsets.UTF_8),
                    ignored -> new ArrayList<>())
                    .add(java.net.URLDecoder.decode(value, java.nio.charset.StandardCharsets.UTF_8));
        }
        return values;
    }

    private static String singleV(Map<String, List<String>> query) {
        List<String> values = query.get("v");
        if (values == null || values.size() != 1 || !DIGITS.matcher(values.get(0)).matches()) {
            return null;
        }
        return values.get(0);
    }

    public static String validateBuild(String value, boolean required) throws ConfigurationException {
        String build = value == null ? "" : value.trim();
        if (build.isEmpty()) {
            if (required) {
                throw new ConfigurationException("a Build number is required when a Build must be pinned");
            }
            return null;
        }
        if (!BUILD.matcher(build).matches() || "latest".equalsIgnoreCase(build)) {
            throw new ConfigurationException("build must be an explicit numeric Build number");
        }
        if (required && !ReliableActor.APPROVED_BUILD_NUMBER.equals(build)) {
            throw new ConfigurationException(
                    "build must be " + ReliableActor.APPROVED_BUILD_NUMBER + " in this client");
        }
        return build;
    }

    public static Double validateChargeCap(String value, boolean required) throws ConfigurationException {
        String trimmed = value == null ? "" : value.trim();
        if (trimmed.isEmpty()) {
            if (required) {
                throw new ConfigurationException("APIFY_MAX_TOTAL_CHARGE_USD is required for capped test mode");
            }
            return null;
        }
        double amount;
        try {
            amount = Double.parseDouble(trimmed);
        } catch (NumberFormatException error) {
            throw new ConfigurationException("APIFY_MAX_TOTAL_CHARGE_USD must be a finite positive number");
        }
        if (Double.isNaN(amount) || Double.isInfinite(amount) || amount <= 0) {
            throw new ConfigurationException("APIFY_MAX_TOTAL_CHARGE_USD must be a finite positive number");
        }
        return amount;
    }

    public static Result getTranscript(String url, Options options) throws ConfigurationException,
            CreationUnknownException, TransportException, ProtocolException {
        String[] validated = validateVideoUrl(url);
        String canonical = validated[0];
        String form = validated[1];

        String token = options.token != null && !options.token.isBlank()
                ? options.token.trim()
                : System.getenv().getOrDefault("APIFY_TOKEN", "").trim();
        if (token.isEmpty()) {
            throw new ConfigurationException("APIFY_TOKEN is required");
        }

        String build = validateBuild(
                options.build == null || options.build.isBlank() ? ReliableActor.APPROVED_BUILD_NUMBER : options.build,
                true);

        String capValue = options.maxTotalChargeUsd;
        if (capValue == null || capValue.isBlank()) {
            capValue = System.getenv().getOrDefault("APIFY_MAX_TOTAL_CHARGE_USD", "");
        }
        boolean requireCap = options.requireChargeCap
                || "1".equals(System.getenv().getOrDefault("APIFY_REQUIRE_MAX_TOTAL_CHARGE_USD", ""));
        Double cap = validateChargeCap(capValue, requireCap);

        ReliableActor.RunOptions runOptions = new ReliableActor.RunOptions();
        runOptions.transport = options.transport != null
                ? options.transport
                : new ReliableActor.HttpTransport(token);
        runOptions.actorId = options.actorId == null || options.actorId.isBlank()
                ? ReliableActor.DEFAULT_ACTOR_ID
                : options.actorId;
        runOptions.input = Map.of("videoUrl", canonical);
        runOptions.sourceUrl = canonical;
        if (options.journal != null) {
            runOptions.journal = options.journal;
        }
        runOptions.timeoutSeconds = options.timeoutSeconds;
        runOptions.runId = options.runId;
        runOptions.rowId = options.rowId;
        runOptions.maxTotalChargeUsd = cap == null ? null : trimNumber(cap);
        runOptions.requireChargeCap = requireCap;
        runOptions.build = build;
        runOptions.requireBuild = true;
        runOptions.datasetLimit = options.datasetLimit;

        ReliableActor.Outcome outcome = ReliableActor.executeInput(runOptions);

        if ("creation_unknown".equals(outcome.status())) {
            throw new CreationUnknownException(
                    "Run creation outcome is unknown (Run ID unavailable): "
                            + firstNonEmpty(outcome.error(), "reconcile before retrying")
                            + "; do not call getTranscript again blindly");
        }
        if ("transport_error".equals(outcome.status())) {
            if ("build_mismatch".equals(outcome.stage())) {
                throw new ProtocolException(
                        "Run was created but its Build could not be verified: "
                                + firstNonEmpty(outcome.error(), "Build mismatch")
                                + "; do not create a replacement Run automatically",
                        outcome.runId(), outcome.datasetId());
            }
            String detail = firstNonEmpty(outcome.error(), "Apify transport failed");
            if (outcome.runId() != null && !outcome.runId().isBlank()) {
                detail += "; Run " + outcome.runId()
                        + " already exists. Reconcile or resume that Run; do not call getTranscript again to replace it";
            }
            throw new TransportException(detail, outcome.runId(), outcome.datasetId());
        }
        if (!ReliableActor.EXIT_CODES.containsKey(outcome.status())) {
            throw new ProtocolException("Runner returned unsupported status: " + outcome.status(),
                    outcome.runId(), outcome.datasetId());
        }

        List<Object> items = outcome.rawItems() == null ? List.of() : outcome.rawItems();
        for (Object item : items) {
            if (!(item instanceof Map)) {
                throw new ProtocolException("Runner returned malformed Dataset items",
                        outcome.runId(), outcome.datasetId());
            }
        }

        if ("run_failed".equals(outcome.status())) {
            return new Result("run_failed", null, List.of(), items, outcome.runId(), outcome.datasetId(),
                    url, canonical, form, firstNonEmpty(outcome.error(), "Actor Run failed"), false,
                    null, null, null);
        }

        Map<String, Object> transcriptItem = null;
        for (Object item : items) {
            Map<String, Object> object = Json.asObject(item);
            if (object.get("text") instanceof String text && !text.isBlank()) {
                transcriptItem = object;
                break;
            }
        }

        String text = null;
        List<Map<String, Object>> segments = List.of();
        if (transcriptItem != null) {
            text = (String) transcriptItem.get("text");
            segments = ReliableActor.normalizeSegments(transcriptItem.get("segments"));
        }
        if ("success".equals(outcome.status()) && (text == null || text.isBlank())) {
            throw new ProtocolException("Runner reported success without a non-empty transcript",
                    outcome.runId(), outcome.datasetId());
        }

        Map<String, Object> metadata = transcriptItem != null
                ? transcriptItem
                : (items.isEmpty() ? Map.of() : Json.asObject(items.get(0)));
        String title = metadata.get("title") instanceof String value ? value : null;
        String nickname = metadata.get("nickname") instanceof String value ? value : null;
        Double duration = metadata.get("duration") instanceof Number number ? number.doubleValue() : null;

        return new Result(outcome.status(), text, segments, items, outcome.runId(), outcome.datasetId(),
                url, canonical, form, outcome.error(), Boolean.TRUE.equals(outcome.datasetComplete()),
                title, nickname, duration);
    }

    private static String trimNumber(double value) {
        return value == Math.rint(value) ? String.valueOf((long) value) : String.valueOf(value);
    }

    private static String firstNonEmpty(String... values) {
        for (String value : values) {
            if (value != null && !value.isBlank()) {
                return value;
            }
        }
        return "";
    }

    /**
     * Locates the nearest .env, walking up from the working directory.
     *
     * <p>The Python and Node clients resolve .env from the repository root; a Java build has no
     * such anchor, so searching upwards keeps {@code java ... Download} working from examples/java
     * while still finding the root file.
     */
    public static java.nio.file.Path findDotEnv() {
        java.nio.file.Path directory = java.nio.file.Path.of("").toAbsolutePath();
        for (int depth = 0; depth <= 4 && directory != null; depth++) {
            java.nio.file.Path candidate = directory.resolve(".env");
            if (java.nio.file.Files.isRegularFile(candidate)) {
                return candidate;
            }
            directory = directory.getParent();
        }
        return java.nio.file.Path.of(".env");
    }

    /**
     * Reads APIFY_* keys from a .env file; the caller decides how to apply them.
     * APIFY_SKIP_DOTENV=1 disables it so tests never pick up a real token.
     */
    public static Map<String, String> readDotEnv(java.nio.file.Path path) {
        Map<String, String> values = new LinkedHashMap<>();
        String skip = System.getenv().getOrDefault("APIFY_SKIP_DOTENV", "").trim();
        if (skip.equals("1") || skip.equalsIgnoreCase("true") || skip.equalsIgnoreCase("yes")) {
            return values;
        }
        if (!java.nio.file.Files.exists(path)) {
            return values;
        }
        try {
            return parseDotEnvLines(java.nio.file.Files.readAllLines(path));
        } catch (IOException ignored) {
            // A missing or unreadable .env is not an error: the environment is enough.
            return values;
        }
    }

    /**
     * Parses already-read {@code .env} lines. Kept separate so the parser can be tested without
     * depending on the {@code APIFY_SKIP_DOTENV} state of the test process.
     */
    static Map<String, String> parseDotEnvLines(java.util.List<String> lines) {
        Map<String, String> values = new LinkedHashMap<>();
        for (String line : lines) {
            String trimmed = line.trim();
            if (trimmed.isEmpty() || trimmed.startsWith("#")) {
                continue;
            }
            int separator = trimmed.indexOf('=');
            if (separator <= 0) {
                continue;
            }
            String key = trimmed.substring(0, separator).trim();
            if (!key.startsWith("APIFY_")) {
                continue;
            }
            String value = trimmed.substring(separator + 1).trim();
            if (value.length() >= 2) {
                char first = value.charAt(0);
                char last = value.charAt(value.length() - 1);
                if ((first == '"' && last == '"') || (first == '\'' && last == '\'')) {
                    value = value.substring(1, value.length() - 1);
                }
            }
            values.put(key, value);
        }
        return values;
    }

    /** Convenience overload used by the CLIs. */
    public static Duration defaultTimeout() {
        return Duration.ofSeconds(ReliableActor.DEFAULT_TIMEOUT_SECONDS);
    }
}
