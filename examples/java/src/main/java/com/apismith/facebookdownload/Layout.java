package com.apismith.facebookdownload;

import java.io.IOException;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Output layout: deterministic folder names and the sidecar files written next to the media.
 *
 * <p>Every language client produces the same layout, so a download started with Python can be
 * resumed or inspected by the Node, Go, or Java examples.
 */
public final class Layout {

    public static final int MANIFEST_SCHEMA_VERSION = 1;
    private static final int TITLE_LIMIT = 48;
    private static final String[] MEDIA_FIELDS = {"videoUrl", "audioUrl", "avatarUri"};
    private static final Pattern PAGE_VIDEO = Pattern.compile("^/([A-Za-z0-9._-]+)/videos/(\\d+)/?$");
    private static final Pattern VIDEOS = Pattern.compile("^/videos/(\\d+)/?$");
    private static final Pattern REEL = Pattern.compile("^/reel/(\\d+)/?$", Pattern.CASE_INSENSITIVE);
    private static final Pattern SHARE = Pattern.compile("^/share/([A-Za-z]+)/([A-Za-z0-9_-]+)/?$");
    private static final Pattern SHORT = Pattern.compile("^/([A-Za-z0-9_-]+)/?$");

    private Layout() {
    }

    public static String utcNow() {
        return Instant.now().truncatedTo(ChronoUnit.SECONDS).toString();
    }

    /** ASCII-only, lower-case, dash-separated slug. Non-ASCII titles degrade to "". */
    public static String slugify(String value, int limit) {
        if (value == null || value.isBlank()) {
            return "";
        }
        int bound = limit > 0 ? limit : TITLE_LIMIT;
        String ascii = java.text.Normalizer.normalize(value, java.text.Normalizer.Form.NFKD)
                .replaceAll("\\p{M}+", "")
                .replaceAll("[^\\x00-\\x7F]", "");
        String slug = ascii.replaceAll("[^A-Za-z0-9]+", "-").replaceAll("^-+|-+$", "").toLowerCase(Locale.ROOT);
        if (slug.length() > bound) {
            slug = slug.substring(0, bound).replaceAll("-+$", "");
        }
        return slug;
    }

    /** Stable identity for the folder name, derived from the canonical URL. */
    public static String videoIdentity(String canonicalUrl, String urlForm) {
        URI uri;
        try {
            uri = URI.create(canonicalUrl);
        } catch (IllegalArgumentException error) {
            return (urlForm == null || urlForm.isEmpty() ? "video" : urlForm) + "-unknown";
        }
        String path = uri.getPath() == null ? "/" : uri.getPath();
        if ("watch".equals(urlForm) || "video_php".equals(urlForm)) {
            String prefix = "watch".equals(urlForm) ? "watch" : "video";
            String id = queryParam(uri.getQuery(), "v");
            return id != null && id.matches("\\d+") ? prefix + "-" + id : prefix + "-unknown";
        }
        Matcher match = PAGE_VIDEO.matcher(path);
        if (match.matches() && "page_video".equals(urlForm)) {
            return "page-" + slugify(match.group(1), 24) + "-" + match.group(2);
        }
        match = VIDEOS.matcher(path);
        if (match.matches()) {
            return "video-" + match.group(1);
        }
        match = REEL.matcher(path);
        if (match.matches()) {
            return "reel-" + match.group(1);
        }
        match = SHARE.matcher(path);
        if (match.matches()) {
            return "share-" + match.group(1).toLowerCase(Locale.ROOT) + "-" + match.group(2);
        }
        if ("short_link".equals(urlForm)) {
            match = SHORT.matcher(path);
            if (match.matches()) {
                return "short-" + match.group(1);
            }
        }
        return (urlForm == null || urlForm.isEmpty() ? "video" : urlForm) + "-unknown";
    }

    private static String queryParam(String query, String key) {
        if (query == null) {
            return null;
        }
        for (String pair : query.split("&")) {
            int equals = pair.indexOf('=');
            if (equals > 0 && pair.substring(0, equals).equals(key)) {
                return pair.substring(equals + 1);
            }
        }
        return null;
    }

    /** Deterministic folder name: identity plus an optional readable title suffix. */
    public static String folderName(String canonicalUrl, String urlForm, String title) {
        String identity = videoIdentity(canonicalUrl, urlForm);
        String suffix = slugify(title, TITLE_LIMIT);
        return suffix.isEmpty() ? identity : identity + "-" + suffix;
    }

    /** Reuses an existing folder for the same URL; never overwrites media from a different one. */
    public static Path chooseOutputDir(Path base, String folder, String canonicalUrl) {
        Path candidate = base.resolve(folder);
        for (int index = 2; ; index++) {
            Path manifest = candidate.resolve("manifest.json");
            if (!Files.isRegularFile(manifest)) {
                return candidate;
            }
            try {
                Object parsed = Json.parse(Files.readString(manifest, StandardCharsets.UTF_8));
                Map<String, Object> record = Json.asObject(parsed);
                Object recorded = record == null ? null : record.get("canonicalUrl");
                if (recorded == null || canonicalUrl.equals(recorded)) {
                    return candidate;
                }
            } catch (IOException | RuntimeException error) {
                return candidate;
            }
            candidate = base.resolve(folder + "-" + index);
        }
    }

    /** Copies an Actor item, reducing signed media URLs to scheme://host/path. */
    public static Map<String, Object> sanitizeItem(Map<String, Object> item, boolean keepMediaUrls) {
        Map<String, Object> cleaned = new LinkedHashMap<>();
        if (item == null) {
            return cleaned;
        }
        cleaned.putAll(item);
        if (keepMediaUrls) {
            return cleaned;
        }
        for (String field : MEDIA_FIELDS) {
            Object value = cleaned.get(field);
            if (value instanceof String text && !text.isEmpty()) {
                String redacted = Media.redactMediaUrl(text);
                if (!redacted.startsWith("[")) {
                    cleaned.put(field, redacted);
                }
            }
        }
        return cleaned;
    }

    /** Keeps only well-formed segments. */
    public static List<Map<String, Object>> transcriptSegments(List<Map<String, Object>> segments) {
        List<Map<String, Object>> result = new ArrayList<>();
        if (segments == null) {
            return result;
        }
        for (Map<String, Object> entry : segments) {
            if (entry == null) {
                continue;
            }
            Object startValue = entry.get("start");
            Object endValue = entry.get("end");
            Object text = entry.get("text");
            if (!(startValue instanceof Number start) || !(endValue instanceof Number end)) {
                continue;
            }
            if (!(text instanceof String)) {
                continue;
            }
            double startNumber = start.doubleValue();
            double endNumber = end.doubleValue();
            if (Double.isNaN(startNumber) || Double.isNaN(endNumber) || startNumber < 0 || endNumber < startNumber) {
                continue;
            }
            Map<String, Object> cleaned = new LinkedHashMap<>();
            cleaned.put("start", startNumber);
            cleaned.put("end", endNumber);
            cleaned.put("text", text);
            result.add(cleaned);
        }
        return result;
    }

    /** Writes UTF-8 text atomically and returns the byte count. */
    public static int writeText(Path target, String text) throws IOException {
        Path parent = target.getParent();
        if (parent != null) {
            Files.createDirectories(parent);
        }
        Path temporary = target.resolveSibling(target.getFileName() + ".tmp");
        byte[] data = text.getBytes(StandardCharsets.UTF_8);
        Files.write(temporary, data);
        Files.move(temporary, target, java.nio.file.StandardCopyOption.REPLACE_EXISTING);
        return data.length;
    }

    /** Writes JSON atomically and returns the byte count. */
    public static int writeJson(Path target, Object payload) throws IOException {
        return writeText(target, Json.write(payload) + "\n");
    }

    /** Assembles manifest.json. No signed URL is ever included. */
    public static Map<String, Object> buildManifest(TranscriptClient.Result result, List<Map<String, Object>> assets,
                                                    int transcriptBytes, int segmentCount,
                                                    List<Map<String, Object>> mediaSources) {
        Map<String, Object> manifest = new LinkedHashMap<>();
        manifest.put("schemaVersion", MANIFEST_SCHEMA_VERSION);
        manifest.put("sourceUrl", result.sourceUrl());
        manifest.put("canonicalUrl", result.canonicalUrl());
        manifest.put("urlForm", result.urlForm());
        manifest.put("status", result.status());
        manifest.put("runId", result.runId());
        manifest.put("datasetId", result.datasetId());
        manifest.put("title", result.title());
        manifest.put("nickname", result.nickname());
        manifest.put("duration", result.duration());
        manifest.put("downloadedAt", utcNow());
        manifest.put("assets", assets);
        Map<String, Object> transcript = new LinkedHashMap<>();
        transcript.put("bytes", transcriptBytes);
        transcript.put("segments", segmentCount);
        manifest.put("transcript", transcript);
        manifest.put("mediaSources", mediaSources);
        manifest.put("notes", List.of(
                "Media links returned by the Actor are signed and short-lived; they are not stored here.",
                "Re-running the same URL reuses this folder and skips files whose size still matches."));
        return manifest;
    }
}
