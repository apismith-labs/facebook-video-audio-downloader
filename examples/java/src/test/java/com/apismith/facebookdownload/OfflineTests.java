package com.apismith.facebookdownload;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Dependency-free test runner: {@code java -cp out com.apismith.facebookdownload.OfflineTests}.
 * Every case is offline and needs no token.
 */
public final class OfflineTests {
    private static int passed;

    public static void main(String[] args) throws Exception {
        urlAcceptance();
        urlRejection();
        buildAndCapValidation();
        classification();
        costModel();
        journalRoundTrip();
        journalRejectsCorruption();
        successRun();
        emptyDataset();
        actorErrorWins();
        creationUnknownOnServerError();
        creationRejectedIsTransportError();
        creationRateLimitedIsNotRetried();
        runFailed();
        buildMismatch();
        resumeIsGetOnly();
        jsonRoundTrip();
        dotEnvQuoteStyles();
        System.out.println(passed + " assertions passed");
    }

    private static void check(boolean condition, String message) {
        if (!condition) {
            throw new AssertionError(message);
        }
        passed++;
    }

    private static void urlAcceptance() throws Exception {
        Map<String, String[]> cases = new LinkedHashMap<>();
        cases.put("https://www.facebook.com/watch/?v=1234567890",
                new String[] {"https://www.facebook.com/watch/?v=1234567890", "watch"});
        cases.put("https://facebook.com/watch?v=1234567890&mibextid=x",
                new String[] {"https://www.facebook.com/watch/?v=1234567890", "watch"});
        cases.put("https://www.facebook.com/SomePage/videos/987654321/",
                new String[] {"https://www.facebook.com/SomePage/videos/987654321/", "page_video"});
        cases.put("https://m.facebook.com/videos/1234567890",
                new String[] {"https://www.facebook.com/videos/1234567890/", "video"});
        cases.put("https://web.facebook.com/reel/1234567890/",
                new String[] {"https://www.facebook.com/reel/1234567890/", "reel"});
        cases.put("https://www.facebook.com/video.php?v=1234567890",
                new String[] {"https://www.facebook.com/video.php?v=1234567890", "video_php"});
        cases.put("https://www.facebook.com/share/v/AbCdEf123/",
                new String[] {"https://www.facebook.com/share/v/AbCdEf123/", "share"});
        cases.put("https://www.facebook.com/share/R/AbCdEf123",
                new String[] {"https://www.facebook.com/share/r/AbCdEf123/", "share"});
        cases.put("https://fb.watch/AbCdEf123/", new String[] {"https://fb.watch/AbCdEf123/", "short_link"});

        for (Map.Entry<String, String[]> entry : cases.entrySet()) {
            String[] result = VideoClient.validateVideoUrl(entry.getKey());
            check(result[0].equals(entry.getValue()[0]), "canonical for " + entry.getKey() + " = " + result[0]);
            check(result[1].equals(entry.getValue()[1]), "form for " + entry.getKey() + " = " + result[1]);
        }
    }

    private static void urlRejection() {
        String[] rejected = {
                "http://www.facebook.com/watch/?v=123",
                "https://www.facebook.com/watch/?v=123#top",
                "https://www.facebook.com/stories/123/",
                "https://www.facebook.com/groups/123/posts/456/",
                "https://example.com/watch/?v=123",
                "https://user:pass@www.facebook.com/watch/?v=123",
                "https://www.facebook.com:8443/watch/?v=123",
                "https://www.facebook.com/watch/",
                "https://www.facebook.com/watch/?v=1&v=2",
                "https://www.facebook.com/watch/?v=abc",
                "https://www.facebook.com/share/x/abc/",
                " https://www.facebook.com/watch/?v=123",
        };
        for (String url : rejected) {
            boolean threw = false;
            try {
                VideoClient.validateVideoUrl(url);
            } catch (VideoClient.ConfigurationException expected) {
                threw = true;
            }
            check(threw, "expected rejection: " + url);
        }
    }

    private static void buildAndCapValidation() throws Exception {
        check(VideoClient.validateBuild("0.0.17", true).equals("0.0.17"), "pinned build accepted");
        check(throwsConfiguration(() -> VideoClient.validateBuild("latest", false)), "latest rejected");
        check(throwsConfiguration(() -> VideoClient.validateBuild("9.9.9", true)), "other build rejected");
        check(throwsConfiguration(() -> VideoClient.validateChargeCap("0", false)), "zero cap rejected");
        check(throwsConfiguration(() -> VideoClient.validateChargeCap("-1", false)), "negative cap rejected");
        check(throwsConfiguration(() -> VideoClient.validateChargeCap("", true)), "missing required cap rejected");
        check(VideoClient.validateChargeCap("0.10", false) == 0.10, "0.10 accepted");
    }

    private static boolean throwsConfiguration(ThrowingCall call) {
        try {
            call.run();
            return false;
        } catch (Exception expected) {
            return true;
        }
    }

    private interface ThrowingCall {
        void run() throws Exception;
    }

    private static void classification() {
        check(ReliableActor.classifyItems(List.of()).status().equals("empty"), "empty dataset");
        check(ReliableActor.classifyItems(null).status().equals("unknown"), "non-list dataset");
        check(ReliableActor.classifyItems(List.of("nope")).status().equals("unknown"), "non-object item");
        check(ReliableActor.classifyItems(List.of(Map.of("errMsg", "private", "videoUrl", "https://x/a.mp4")))
                .status().equals("actor_error"), "errMsg wins over media");
        check(ReliableActor.classifyItems(List.of(Map.of("errMsg", "  ", "videoUrl", "https://x/a.mp4")))
                .status().equals("success"), "blank errMsg ignored");

        Map<String, Object> nullUrl = new LinkedHashMap<>();
        nullUrl.put("videoUrl", null);
        check(ReliableActor.classifyItems(List.of(nullUrl)).status().equals("unknown"), "null url");
        check(ReliableActor.classifyItems(List.of(Map.of("title", "x"))).status().equals("unknown"),
                "absent url");
        check(ReliableActor.classifyItems(List.of(Map.of("videoUrl", "   "))).status().equals("unknown"),
                "blank url");
        check(ReliableActor.classifyItems(List.of(Map.of("videoUrl", 42))).status().equals("unknown"),
                "numeric url");
    }

    private static void costModel() {
        check(Math.abs(Cost.estimate(1) - 0.005) < 1e-9, "one video");
        check(Math.abs(Cost.estimate(1000) - 5.00) < 1e-9, "1000 videos");
        check(Math.abs(Cost.estimate(0)) < 1e-9, "zero videos");
        check(throwsConfiguration(() -> Cost.estimate(-1)), "negative videos rejected");
    }

    private static void journalRoundTrip() throws Exception {
        Path directory = Files.createTempDirectory("fb-journal");
        ReliableActor.FileJournal journal = new ReliableActor.FileJournal(directory.resolve("journal.jsonl"));
        journal.append(Map.of("runId", "run-1", "stage", "creating"));
        journal.append(Map.of("runId", "run-1", "stage", "run_created"));

        Map<String, Object> latest = journal.latest("run-1");
        check(latest != null && "run_created".equals(latest.get("stage")), "journal latest record");
        check(journal.latest("run-2") == null, "unknown run id returns null");
    }

    private static void journalRejectsCorruption() throws Exception {
        Path directory = Files.createTempDirectory("fb-journal-corrupt");
        Path path = directory.resolve("journal.jsonl");
        Files.writeString(path, "{not json}\n");
        ReliableActor.FileJournal journal = new ReliableActor.FileJournal(path);
        boolean rejected = false;
        try {
            journal.append(Map.of("runId", "run-1"));
        } catch (java.io.IOException expected) {
            rejected = true;
        }
        check(rejected, "corrupt journal must be rejected");
    }

    private static final class FakeTransport implements ReliableActor.Transport {
        private final List<String> calls = new ArrayList<>();
        private final Handler handler;

        interface Handler {
            Object handle(String method, String path) throws ReliableActor.ApiException;
        }

        FakeTransport(Handler handler) {
            this.handler = handler;
        }

        @Override
        public Object request(String method, String path, Object body, Map<String, String> query)
                throws ReliableActor.ApiException {
            calls.add(method + " " + path);
            return handler.handle(method, path);
        }

        long count(String method) {
            return calls.stream().filter(call -> call.startsWith(method + " ")).count();
        }
    }

    private static Map<String, Object> createdRun(String buildNumber, String buildId) {
        Map<String, Object> data = new LinkedHashMap<>();
        data.put("id", "run-1");
        data.put("status", "READY");
        data.put("defaultDatasetId", "ds-1");
        data.put("buildNumber", buildNumber);
        data.put("buildId", buildId);
        return Map.of("data", data);
    }

    private static Map<String, Object> succeededRun(String status) {
        Map<String, Object> data = new LinkedHashMap<>();
        data.put("id", "run-1");
        data.put("status", status);
        data.put("defaultDatasetId", "ds-1");
        data.put("buildNumber", ReliableActor.APPROVED_BUILD_NUMBER);
        data.put("buildId", ReliableActor.APPROVED_BUILD_ID);
        return Map.of("data", data);
    }

    private static Map<String, Object> mediaItem() {
        Map<String, Object> item = new LinkedHashMap<>();
        item.put("url", "https://www.facebook.com/watch/?v=1115932820735409");
        item.put("title", "Q & A about sleeping");
        item.put("duration", 43.5);
        item.put("nickname", "Dr. Shu");
        item.put("videoUrl", "https://scontent.xx.fbcdn.net/v/a.mp4?oh=1");
        item.put("audioUrl", "https://scontent.xx.fbcdn.net/v/b.mp4?oh=1");
        item.put("errMsg", "");
        item.put("likesCount", 75000);
        item.put("shareCount", 1200);
        return item;
    }

    private static FakeTransport happyTransport(List<Object> items) {
        return new FakeTransport((method, path) -> {
            if ("POST".equals(method)) {
                return createdRun(ReliableActor.APPROVED_BUILD_NUMBER, ReliableActor.APPROVED_BUILD_ID);
            }
            if (path.startsWith("/actor-runs/")) {
                return succeededRun("SUCCEEDED");
            }
            if (path.startsWith("/datasets/")) {
                return items;
            }
            throw new ReliableActor.ApiException("unexpected path " + path, 404, 0, false);
        });
    }

    private static VideoClient.Options options(ReliableActor.Transport transport) {
        VideoClient.Options options = new VideoClient.Options();
        options.token = "test-token";
        options.transport = transport;
        options.timeoutSeconds = 5;
        return options;
    }

    private static void successRun() throws Exception {
        FakeTransport transport = happyTransport(List.of(mediaItem()));
        VideoClient.Result result = VideoClient.resolveMedia(
                "https://www.facebook.com/watch/?v=1234567890", options(transport));

        check(result.status().equals("success"), "status = " + result.status());
        check("run-1".equals(result.runId()), "run id");
        check("ds-1".equals(result.datasetId()), "dataset id");
        check(result.datasetComplete(), "dataset complete");
        check(result.duration() != null && result.duration() == 43.5, "duration");
        check(result.items().size() == 1, "raw items kept");
        check(result.exitCode() == 0, "exit code");
    }

    private static void emptyDataset() throws Exception {
        VideoClient.Result result = VideoClient.resolveMedia(
                "https://www.facebook.com/reel/1234567890/", options(happyTransport(List.of())));
        check(result.status().equals("empty"), "empty status");
        check(result.exitCode() == 5, "empty exit code");
    }

    private static void actorErrorWins() throws Exception {
        Map<String, Object> item = new LinkedHashMap<>(mediaItem());
        item.put("errMsg", "video is private");
        VideoClient.Result result = VideoClient.resolveMedia(
                "https://www.facebook.com/watch/?v=1234567890", options(happyTransport(List.of(item))));
        check(result.status().equals("actor_error"), "actor_error status");
        check(result.exitCode() == 4, "actor_error exit code");
    }

    private static void creationUnknownOnServerError() {
        FakeTransport transport = new FakeTransport((method, path) -> {
            throw new ReliableActor.ApiException("Apify HTTP 500", 500, 0, true);
        });
        boolean threw = false;
        try {
            VideoClient.resolveMedia("https://www.facebook.com/watch/?v=1", options(transport));
        } catch (VideoClient.CreationUnknownException expected) {
            threw = true;
        } catch (Exception other) {
            throw new AssertionError("unexpected exception " + other);
        }
        check(threw, "500 on create must be creation_unknown");
        check(transport.count("POST") == 1, "POST attempted once");
    }

    private static void creationRejectedIsTransportError() {
        FakeTransport transport = new FakeTransport((method, path) -> {
            throw new ReliableActor.ApiException("Apify HTTP 400", 400, 0, false);
        });
        boolean threw = false;
        try {
            VideoClient.resolveMedia("https://www.facebook.com/watch/?v=1", options(transport));
        } catch (VideoClient.TransportException expected) {
            threw = true;
        } catch (Exception other) {
            throw new AssertionError("unexpected exception " + other);
        }
        check(threw, "400 on create must be transport_error");
        check(transport.count("POST") == 1, "POST attempted once");
    }

    private static void creationRateLimitedIsNotRetried() {
        FakeTransport transport = new FakeTransport((method, path) -> {
            throw new ReliableActor.ApiException("Apify HTTP 429", 429, 0, false);
        });
        boolean threw = false;
        try {
            VideoClient.resolveMedia("https://www.facebook.com/watch/?v=1", options(transport));
        } catch (VideoClient.CreationUnknownException expected) {
            threw = true;
        } catch (Exception other) {
            throw new AssertionError("unexpected exception " + other);
        }
        check(threw, "429 on create must be creation_unknown");
        check(transport.count("POST") == 1, "429 on create is never retried");
    }

    private static void runFailed() throws Exception {
        FakeTransport transport = new FakeTransport((method, path) -> {
            if ("POST".equals(method)) {
                return createdRun(ReliableActor.APPROVED_BUILD_NUMBER, ReliableActor.APPROVED_BUILD_ID);
            }
            return succeededRun("FAILED");
        });
        VideoClient.Result result = VideoClient.resolveMedia(
                "https://www.facebook.com/watch/?v=1", options(transport));
        check(result.status().equals("run_failed"), "run_failed status");
        check(result.exitCode() == 9, "run_failed exit code");
    }

    private static void buildMismatch() {
        FakeTransport transport = new FakeTransport((method, path) -> {
            if ("POST".equals(method)) {
                return createdRun("0.0.19", "someOtherBuildId");
            }
            throw new ReliableActor.ApiException("unexpected path", 404, 0, false);
        });
        boolean threw = false;
        try {
            VideoClient.resolveMedia("https://www.facebook.com/watch/?v=1", options(transport));
        } catch (VideoClient.ProtocolException expected) {
            threw = true;
        } catch (Exception other) {
            throw new AssertionError("unexpected exception " + other);
        }
        check(threw, "build mismatch must be a protocol error");
        check(transport.count("POST") == 1, "build mismatch never creates a second Run");
    }

    private static void resumeIsGetOnly() throws Exception {
        FakeTransport transport = new FakeTransport((method, path) -> {
            if (path.startsWith("/actor-runs/")) {
                return succeededRun("SUCCEEDED");
            }
            if (path.startsWith("/datasets/")) {
                return List.of(mediaItem());
            }
            throw new ReliableActor.ApiException("unexpected path " + path, 404, 0, false);
        });
        VideoClient.Options options = options(transport);
        options.runId = "run-1";
        VideoClient.Result result = VideoClient.resolveMedia(
                "https://www.facebook.com/watch/?v=1", options);
        check(result.status().equals("success"), "resume status");
        check(transport.count("POST") == 0, "resume must not POST");
    }

    private static void jsonRoundTrip() {
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("text", "hello \"world\"\nline");
        payload.put("count", 3);
        payload.put("nested", List.of(Map.of("start", 0.5, "end", 1.0)));
        String encoded = Json.write(payload);
        Map<String, Object> decoded = Json.asObject(Json.parse(encoded));
        check(decoded.get("text").equals("hello \"world\"\nline"), "json string round trip");
        check(((Number) decoded.get("count")).intValue() == 3, "json number round trip");
        check(Json.asList(decoded.get("nested")).size() == 1, "json array round trip");
    }

    private static void dotEnvQuoteStyles() {
        // `.env` files in the wild quote values with either style; both must reach the validator bare.
        Map<String, String> values = VideoClient.parseDotEnvLines(List.of(
                "# comment",
                "APIFY_TOKEN='single-quoted'",
                "APIFY_MAX_TOTAL_CHARGE_USD=\"0.10\"",
                "APIFY_ACTOR_ID=plain",
                "NOT_APIFY=ignored",
                ""));
        check("single-quoted".equals(values.get("APIFY_TOKEN")), "single quotes must be stripped");
        check("0.10".equals(values.get("APIFY_MAX_TOTAL_CHARGE_USD")), "double quotes must be stripped");
        check("plain".equals(values.get("APIFY_ACTOR_ID")), "unquoted values must be preserved");
        check(!values.containsKey("NOT_APIFY"), "only APIFY_ keys are read");
        check(values.size() == 3, "unexpected keys: " + values.keySet());
    }

}
