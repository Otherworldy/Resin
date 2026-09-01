import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { getNode, listNodes } from "../../features/nodes/api";
import type { NodeSummary } from "../../features/nodes/types";
import { Input } from "../../components/ui/Input";
import { cn } from "../../lib/cn";

type RelayNodeSelectProps = {
  value?: string;
  onChange: (hash: string) => void;
  invalid?: boolean;
};

// Node-level latency shown beside each option: latest probe result if
// available, otherwise the authority reference latency; "-" when unknown.
function nodeLatencyMs(node: NodeSummary): number | null {
  if (typeof node.last_probe_latency_ms === "number") {
    return node.last_probe_latency_ms;
  }
  if (typeof node.reference_latency_ms === "number") {
    return node.reference_latency_ms;
  }
  return null;
}

function latencyColor(latencyMs: number): string {
  if (!Number.isFinite(latencyMs)) {
    return "var(--text-secondary)";
  }
  if (latencyMs <= 400) {
    return "var(--success)";
  }
  if (latencyMs <= 1000) {
    return "var(--warning)";
  }
  return "var(--danger)";
}

function formatLatency(latencyMs: number | null): string {
  if (latencyMs === null || !Number.isFinite(latencyMs)) {
    return "-";
  }
  return `${Math.round(latencyMs)} ms`;
}

/**
 * Searchable node picker for the platform relay (中转节点) field.
 *
 * Node pools can be large, so the option list is fetched from the server with
 * a debounced tag-keyword search instead of loading every node up front:
 * - empty query: first 50 enabled nodes with an outbound, sorted by tag
 * - keyword: server-side fuzzy match on the node display tag
 * - the currently selected node is resolved on demand via GET /nodes/{hash}
 */
export function RelayNodeSelect({ value, onChange, invalid }: RelayNodeSelectProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [keyword, setKeyword] = useState("");
  const [debounced, setDebounced] = useState("");
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(keyword.trim()), 300);
    return () => clearTimeout(timer);
  }, [keyword]);

  useEffect(() => {
    const onDown = (event: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target as Node)) {
        setOpen(false);
        setKeyword("");
      }
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, []);

  const optionsQuery = useQuery({
    queryKey: ["nodes", "relay-options", debounced],
    queryFn: () =>
      listNodes({
        tag_keyword: debounced || undefined,
        has_outbound: true,
        enabled: true,
        limit: 50,
        sort_by: "tag",
      }),
    staleTime: 30_000,
  });

  // Resolve the current selection's display tag when it isn't in the options.
  const selectedQuery = useQuery({
    queryKey: ["nodes", "relay-selected", value],
    queryFn: () => getNode(value as string),
    enabled: !!value,
    staleTime: 60_000,
  });

  const selected = useMemo(
    () =>
      value
        ? optionsQuery.data?.items.find((item) => item.node_hash === value) ??
          (selectedQuery.data?.node_hash === value ? selectedQuery.data : undefined)
        : undefined,
    [value, optionsQuery.data, selectedQuery.data],
  );

  const select = (hash: string) => {
    onChange(hash || "");
    setOpen(false);
    setKeyword("");
  };

  const showOptions = open;
  const inputValue = open ? keyword : selected?.display_tag || "";

  return (
    <div className="relay-select" ref={wrapRef}>
      <Input
        value={inputValue}
        invalid={invalid}
        placeholder={t("搜索或选择中转节点（留空=直连）")}
        onFocus={() => setOpen(true)}
        onChange={(event) => {
          setKeyword(event.target.value);
          setOpen(true);
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            setOpen(false);
            setKeyword("");
          }
        }}
      />
      {showOptions ? (
        <div className="relay-select-options">
          <button
            type="button"
            className="relay-select-option relay-select-direct"
            onClick={() => select("")}
          >
            {t("直连（不使用中转节点）")}
          </button>
          {(optionsQuery.data?.items ?? []).map((item) => (
            <button
              key={item.node_hash}
              type="button"
              className={cn(
                "relay-select-option",
                item.node_hash === value && "relay-select-option-active",
              )}
              onClick={() => select(item.node_hash)}
            >
              <span className="relay-select-option-tag">{item.display_tag || item.node_hash}</span>
              <span className="relay-select-option-meta">
                {nodeLatencyMs(item) !== null ? (
                  <span className="relay-select-option-latency" style={{ color: latencyColor(nodeLatencyMs(item) as number) }}>
                    {formatLatency(nodeLatencyMs(item))}
                  </span>
                ) : null}
                <span className="relay-select-option-hash">{item.node_hash.slice(0, 8)}</span>
              </span>
            </button>
          ))}
          {optionsQuery.data && optionsQuery.data.items.length === 0 ? (
            <div className="relay-select-empty">{t("无匹配节点")}</div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
