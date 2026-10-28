package com.apismith.facebookdownload;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;

/** Two-event pricing model: $0.001 per item plus a per-started-minute fee. */
public final class Cost {
    public static final double DATASET_ITEM_USD = 0.001;

    public static final Map<String, Double> TIER_PRICES_USD;

    static {
        Map<String, Double> prices = new LinkedHashMap<>();
        prices.put("FREE", 0.0045);
        prices.put("BRONZE", 0.0035);
        prices.put("SILVER", 0.003);
        prices.put("GOLD", 0.002);
        prices.put("PLATINUM", 0.002);
        prices.put("DIAMOND", 0.002);
        TIER_PRICES_USD = java.util.Collections.unmodifiableMap(prices);
    }

    public static final List<String> TIERS = List.copyOf(TIER_PRICES_USD.keySet());

    private Cost() {
    }

    /** Any duration under a minute is billed as a full minute. */
    public static int billedMinutes(double seconds) {
        if (seconds <= 0) {
            return 1;
        }
        double minutes = Math.ceil(seconds / 60.0);
        return (int) Math.max(1, minutes);
    }

    public static double estimate(int videos, double secondsPerVideo, String tier) {
        Double price = TIER_PRICES_USD.get(tier.toUpperCase());
        if (price == null) {
            throw new IllegalArgumentException("unknown tier: " + tier);
        }
        if (videos < 0 || secondsPerVideo < 0) {
            throw new IllegalArgumentException("videos and duration must not be negative");
        }
        return videos * (DATASET_ITEM_USD + billedMinutes(secondsPerVideo) * price);
    }

    public static String formatUsd(double amount) {
        return String.format("$%.4f", amount);
    }

    /** Deterministic summary used by the batch planner and the CLI. */
    public static Map<String, Object> summary(int videos, double seconds, String tier) {
        Map<String, Object> result = new TreeMap<>();
        result.put("videos", videos);
        result.put("secondsPerVideo", seconds);
        result.put("tier", tier.toUpperCase());
        result.put("billedMinutesPerVideo", billedMinutes(seconds));
        result.put("estimatedUsd", estimate(videos, seconds, tier));
        return result;
    }
}
