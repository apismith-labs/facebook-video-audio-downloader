package com.apismith.facebookdownload;

import java.util.LinkedHashMap;
import java.util.Map;

/**
 * Flat per-result pricing for the Facebook Video &amp; Audio Downloader Actor.
 *
 * <p>Prices captured on 2026-11-20. A Run is billed per dataset item it writes, including a Run
 * whose item carries {@code errMsg}.
 */
public final class Cost {
    public static final double RESULT_USD = 0.005;
    public static final String PRICING_CAPTURED_AT = "2026-11-20";

    private Cost() {
    }

    public static double estimate(int videos) {
        if (videos < 0) {
            throw new IllegalArgumentException("videos must not be negative");
        }
        return videos * RESULT_USD;
    }

    public static String formatUsd(double amount) {
        return String.format("$%.4f", amount);
    }

    /** Deterministic summary used by the batch planner and the CLI. */
    public static Map<String, Object> summary(int videos) {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("videos", videos);
        result.put("resultUsd", RESULT_USD);
        result.put("totalUsd", estimate(videos));
        return result;
    }
}
