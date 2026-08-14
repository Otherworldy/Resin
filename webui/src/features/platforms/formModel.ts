import { z } from "zod";
import { allocationPolicies, emptyAccountBehaviors, missActions } from "./constants";
import { parseHeaderLines, parseLinesToList } from "./formParsers";
import type { Platform, PlatformCreateInput, PlatformProbeOverride, PlatformUpdateInput } from "./types";

const platformNameForbiddenChars = ".:|/\\@?#%~";
const platformNameForbiddenSpacing = " \t\r\n";
const platformNameReserved = "api";

function containsAny(source: string, chars: string): boolean {
  for (const ch of chars) {
    if (source.includes(ch)) {
      return true;
    }
  }
  return false;
}

export const platformNameRuleHint = "平台名不能包含 .:|/\\@?#%~、空格、Tab、换行、回车，也不能为保留字。";

export const platformFormSchema = z.object({
  name: z.string().trim()
    .min(1, "平台名称不能为空")
    .refine((value) => !containsAny(value, platformNameForbiddenChars), {
      message: "平台名称不能包含字符 .:|/\\@?#%~",
    })
    .refine((value) => !containsAny(value, platformNameForbiddenSpacing), {
      message: "平台名称不能包含空格、Tab、换行、回车",
    })
    .refine((value) => value.toLowerCase() !== platformNameReserved, {
      message: "平台名称不能为保留字",
    }),
  sticky_ttl: z.string().optional(),
  regex_filters_text: z.string().optional(),
  region_filters_text: z.string().optional(),
  reverse_proxy_miss_action: z.enum(missActions),
  reverse_proxy_empty_account_behavior: z.enum(emptyAccountBehaviors),
  reverse_proxy_fixed_account_header: z.string().optional(),
  allocation_policy: z.enum(allocationPolicies),
  passive_circuit_breaker_disabled: z.boolean(),
  max_node_latency: z.string().optional(),
  probe_disabled: z.boolean(),
  probe_latency_interval: z.string().optional(),
  probe_egress_interval: z.string().optional(),
  probe_latency_test_url: z.string().optional(),
}).superRefine((value, ctx) => {
  if (
    value.reverse_proxy_empty_account_behavior === "FIXED_HEADER" &&
    parseHeaderLines(value.reverse_proxy_fixed_account_header).length === 0
  ) {
    ctx.addIssue({
      code: "custom",
      path: ["reverse_proxy_fixed_account_header"],
      message: "用于提取 Account 的 Headers 不能为空",
    });
  }
  if (Number.isNaN(parseGoDurationNs(value.probe_latency_interval ?? ""))) {
    ctx.addIssue({
      code: "custom",
      path: ["probe_latency_interval"],
      message: "延迟探测间隔格式无效，例如 30m、1h",
    });
  }
  if (Number.isNaN(parseGoDurationNs(value.probe_egress_interval ?? ""))) {
    ctx.addIssue({
      code: "custom",
      path: ["probe_egress_interval"],
      message: "出口探测间隔格式无效，例如 6h、24h",
    });
  }
  if (Number.isNaN(parseGoDurationNs(value.max_node_latency ?? ""))) {
    ctx.addIssue({
      code: "custom",
      path: ["max_node_latency"],
      message: "最大节点延迟格式无效，例如 500ms、2s；留空表示不限制",
    });
  }
});

export type PlatformFormValues = z.infer<typeof platformFormSchema>;

export const defaultPlatformFormValues: PlatformFormValues = {
  name: "",
  sticky_ttl: "",
  regex_filters_text: "",
  region_filters_text: "",
  reverse_proxy_miss_action: "TREAT_AS_EMPTY",
  reverse_proxy_empty_account_behavior: "RANDOM",
  reverse_proxy_fixed_account_header: "Authorization",
  allocation_policy: "BALANCED",
  passive_circuit_breaker_disabled: false,
  max_node_latency: "",
  probe_disabled: false,
  probe_latency_interval: "",
  probe_egress_interval: "",
  probe_latency_test_url: "",
};

export function platformToFormValues(platform: Platform): PlatformFormValues {
  const regexFilters = Array.isArray(platform.regex_filters) ? platform.regex_filters : [];
  const regionFilters = Array.isArray(platform.region_filters) ? platform.region_filters : [];

  return {
    name: platform.name,
    sticky_ttl: platform.sticky_ttl,
    regex_filters_text: regexFilters.join("\n"),
    region_filters_text: regionFilters.join("\n"),
    reverse_proxy_miss_action: platform.reverse_proxy_miss_action,
    reverse_proxy_empty_account_behavior: platform.reverse_proxy_empty_account_behavior,
    reverse_proxy_fixed_account_header: platform.reverse_proxy_fixed_account_header,
    allocation_policy: platform.allocation_policy,
    passive_circuit_breaker_disabled: platform.passive_circuit_breaker_disabled,
    max_node_latency: platform.max_node_latency === "0s" ? "" : platform.max_node_latency,
    probe_disabled: platform.probe_override?.disabled ?? false,
    probe_latency_interval: platform.probe_override?.latency_probe_interval_ns
      ? formatNsAsGoDuration(platform.probe_override.latency_probe_interval_ns)
      : "",
    probe_egress_interval: platform.probe_override?.egress_probe_interval_ns
      ? formatNsAsGoDuration(platform.probe_override.egress_probe_interval_ns)
      : "",
    probe_latency_test_url: platform.probe_override?.latency_test_url ?? "",
  };
}

// parseGoDurationNs converts a Go-style duration string (e.g. "30m", "1h30m")
// to nanoseconds. Returns 0 when the input is empty.
export function parseGoDurationNs(raw: string): number {
  const input = raw.trim();
  if (!input) {
    return 0;
  }
  const pattern = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let totalNs = 0;
  let consumed = 0;
  let match: RegExpExecArray | null;
  const unitNs: Record<string, number> = { ns: 1, us: 1000, µs: 1000, ms: 1e6, s: 1e9, m: 60e9, h: 3600e9 };
  while ((match = pattern.exec(input)) !== null) {
    totalNs += Number(match[1]) * (unitNs[match[2]] ?? 0);
    consumed += match[0].length;
  }
  if (consumed !== input.length || totalNs < 0) {
    return Number.NaN;
  }
  return Math.round(totalNs);
}

// formatNsAsGoDuration converts nanoseconds back to a compact Go duration string.
export function formatNsAsGoDuration(ns: number): string {
  if (!Number.isFinite(ns) || ns <= 0) {
    return "";
  }
  if (ns % 3600e9 === 0) {
    return `${ns / 3600e9}h`;
  }
  if (ns % 60e9 === 0) {
    return `${ns / 60e9}m`;
  }
  if (ns % 1e9 === 0) {
    return `${ns / 1e9}s`;
  }
  return `${ns / 1e6}ms`;
}

function toPlatformPayloadBase(values: PlatformFormValues) {
  const latencyNs = parseGoDurationNs(values.probe_latency_interval ?? "");
  const egressNs = parseGoDurationNs(values.probe_egress_interval ?? "");
  const probeOverride: PlatformProbeOverride = {};
  if (values.probe_disabled) {
    probeOverride.disabled = true;
  }
  if (latencyNs > 0) {
    probeOverride.latency_probe_interval_ns = latencyNs;
  }
  if (egressNs > 0) {
    probeOverride.egress_probe_interval_ns = egressNs;
  }
  const testURL = values.probe_latency_test_url?.trim() ?? "";
  if (testURL) {
    probeOverride.latency_test_url = testURL;
  }

  return {
    name: values.name.trim(),
    regex_filters: parseLinesToList(values.regex_filters_text),
    region_filters: parseLinesToList(values.region_filters_text, (value) => value.toLowerCase()),
    reverse_proxy_miss_action: values.reverse_proxy_miss_action,
    reverse_proxy_empty_account_behavior: values.reverse_proxy_empty_account_behavior,
    reverse_proxy_fixed_account_header: parseHeaderLines(values.reverse_proxy_fixed_account_header).join("\n"),
    allocation_policy: values.allocation_policy,
    passive_circuit_breaker_disabled: values.passive_circuit_breaker_disabled,
    max_node_latency: values.max_node_latency?.trim() || "0s",
    // 空对象代表清除平台探测覆盖（回退到全局探测配置）。
    probe_override: Object.keys(probeOverride).length > 0 ? probeOverride : {},
  };
}

export function toPlatformCreateInput(values: PlatformFormValues): PlatformCreateInput {
  const { probe_override: probeOverride, ...rest } = toPlatformPayloadBase(values);
  return {
    ...rest,
    sticky_ttl: values.sticky_ttl?.trim() || undefined,
    max_node_latency: values.max_node_latency?.trim() || undefined,
    // 创建时探测覆盖为空则不传。
    ...(Object.keys(probeOverride).length > 0 ? { probe_override: probeOverride } : {}),
  };
}

export function toPlatformUpdateInput(values: PlatformFormValues): PlatformUpdateInput {
  return {
    ...toPlatformPayloadBase(values),
    sticky_ttl: values.sticky_ttl?.trim() || "",
  };
}
