package com.apismith.facebookdownload;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Minimal JSON reader/writer so this example needs no third-party dependency.
 * Supports the subset Apify returns: objects, arrays, strings, numbers,
 * booleans and null.
 */
public final class Json {
    private final String source;
    private int index;

    private Json(String source) {
        this.source = source;
    }

    public static Object parse(String text) {
        Json parser = new Json(text);
        parser.skipWhitespace();
        Object value = parser.readValue();
        parser.skipWhitespace();
        if (parser.index != parser.source.length()) {
            throw new IllegalArgumentException("Trailing content in JSON at offset " + parser.index);
        }
        return value;
    }

    private Object readValue() {
        if (index >= source.length()) {
            throw new IllegalArgumentException("Unexpected end of JSON");
        }
        char current = source.charAt(index);
        return switch (current) {
            case '{' -> readObject();
            case '[' -> readArray();
            case '"' -> readString();
            case 't' -> readLiteral("true", Boolean.TRUE);
            case 'f' -> readLiteral("false", Boolean.FALSE);
            case 'n' -> readLiteral("null", null);
            default -> readNumber();
        };
    }

    private Map<String, Object> readObject() {
        Map<String, Object> result = new LinkedHashMap<>();
        expect('{');
        skipWhitespace();
        if (peek() == '}') {
            index++;
            return result;
        }
        while (true) {
            skipWhitespace();
            String key = readString();
            skipWhitespace();
            expect(':');
            skipWhitespace();
            result.put(key, readValue());
            skipWhitespace();
            char next = peek();
            if (next == ',') {
                index++;
                continue;
            }
            expect('}');
            return result;
        }
    }

    private List<Object> readArray() {
        List<Object> result = new ArrayList<>();
        expect('[');
        skipWhitespace();
        if (peek() == ']') {
            index++;
            return result;
        }
        while (true) {
            skipWhitespace();
            result.add(readValue());
            skipWhitespace();
            char next = peek();
            if (next == ',') {
                index++;
                continue;
            }
            expect(']');
            return result;
        }
    }

    private String readString() {
        expect('"');
        StringBuilder builder = new StringBuilder();
        while (index < source.length()) {
            char current = source.charAt(index++);
            if (current == '"') {
                return builder.toString();
            }
            if (current != '\\') {
                builder.append(current);
                continue;
            }
            char escape = source.charAt(index++);
            switch (escape) {
                case '"' -> builder.append('"');
                case '\\' -> builder.append('\\');
                case '/' -> builder.append('/');
                case 'b' -> builder.append('\b');
                case 'f' -> builder.append('\f');
                case 'n' -> builder.append('\n');
                case 'r' -> builder.append('\r');
                case 't' -> builder.append('\t');
                case 'u' -> {
                    String hex = source.substring(index, index + 4);
                    builder.append((char) Integer.parseInt(hex, 16));
                    index += 4;
                }
                default -> throw new IllegalArgumentException("Unsupported escape \\" + escape);
            }
        }
        throw new IllegalArgumentException("Unterminated JSON string");
    }

    private Object readLiteral(String literal, Object value) {
        if (!source.startsWith(literal, index)) {
            throw new IllegalArgumentException("Invalid JSON literal at offset " + index);
        }
        index += literal.length();
        return value;
    }

    private Number readNumber() {
        int start = index;
        if (peek() == '-') {
            index++;
        }
        while (index < source.length()
                && "0123456789.eE+-".indexOf(source.charAt(index)) >= 0) {
            index++;
        }
        String text = source.substring(start, index);
        if (text.isEmpty()) {
            throw new IllegalArgumentException("Invalid JSON value at offset " + start);
        }
        if (text.contains(".") || text.contains("e") || text.contains("E")) {
            return Double.parseDouble(text);
        }
        long parsed = Long.parseLong(text);
        return parsed;
    }

    private void skipWhitespace() {
        while (index < source.length() && Character.isWhitespace(source.charAt(index))) {
            index++;
        }
    }

    private char peek() {
        if (index >= source.length()) {
            return '\0';
        }
        return source.charAt(index);
    }

    private void expect(char expected) {
        if (peek() != expected) {
            throw new IllegalArgumentException(
                    "Expected '" + expected + "' at offset " + index + " but found '" + peek() + "'");
        }
        index++;
    }

    public static String write(Object value) {
        StringBuilder builder = new StringBuilder();
        writeValue(builder, value);
        return builder.toString();
    }

    @SuppressWarnings("unchecked")
    private static void writeValue(StringBuilder builder, Object value) {
        if (value == null) {
            builder.append("null");
            return;
        }
        if (value instanceof String text) {
            writeString(builder, text);
            return;
        }
        if (value instanceof Boolean || value instanceof Integer || value instanceof Long) {
            builder.append(value);
            return;
        }
        if (value instanceof Number number) {
            double asDouble = number.doubleValue();
            if (Double.isNaN(asDouble) || Double.isInfinite(asDouble)) {
                builder.append("null");
            } else if (asDouble == Math.rint(asDouble) && Math.abs(asDouble) < 1e15) {
                builder.append((long) asDouble);
            } else {
                builder.append(asDouble);
            }
            return;
        }
        if (value instanceof Map<?, ?> map) {
            builder.append('{');
            boolean first = true;
            for (Map.Entry<?, ?> entry : map.entrySet()) {
                if (!first) {
                    builder.append(',');
                }
                first = false;
                writeString(builder, String.valueOf(entry.getKey()));
                builder.append(':');
                writeValue(builder, entry.getValue());
            }
            builder.append('}');
            return;
        }
        if (value instanceof List<?> list) {
            builder.append('[');
            for (int position = 0; position < list.size(); position++) {
                if (position > 0) {
                    builder.append(',');
                }
                writeValue(builder, list.get(position));
            }
            builder.append(']');
            return;
        }
        writeString(builder, String.valueOf(value));
    }

    private static void writeString(StringBuilder builder, String text) {
        builder.append('"');
        for (int position = 0; position < text.length(); position++) {
            char current = text.charAt(position);
            switch (current) {
                case '"' -> builder.append("\\\"");
                case '\\' -> builder.append("\\\\");
                case '\n' -> builder.append("\\n");
                case '\r' -> builder.append("\\r");
                case '\t' -> builder.append("\\t");
                default -> {
                    if (current < 0x20) {
                        builder.append(String.format("\\u%04x", (int) current));
                    } else {
                        builder.append(current);
                    }
                }
            }
        }
        builder.append('"');
    }

    /** Convenience accessor for object values. */
    @SuppressWarnings("unchecked")
    public static Map<String, Object> asObject(Object value) {
        if (value instanceof Map<?, ?> map) {
            return (Map<String, Object>) map;
        }
        return Map.of();
    }

    /** Convenience accessor for array values. */
    @SuppressWarnings("unchecked")
    public static List<Object> asList(Object value) {
        if (value instanceof List<?> list) {
            return (List<Object>) list;
        }
        return null;
    }
}
