import type { UseFormReturn } from "react-hook-form";
import { Info } from "lucide-react";
import { Input } from "../../components/ui/Input";
import { Switch } from "../../components/ui/Switch";
import type { PlatformFormValues } from "./formModel";

type Props = {
  idPrefix: string;
  form: UseFormReturn<PlatformFormValues>;
  t: (text: string) => string;
};

export function ProbeOverrideFields({ idPrefix, form, t }: Props) {
  const {
    register,
    formState: { errors },
  } = form;

  return (
    <>
      <div className="field-group">
        <label className="field-label" htmlFor={`${idPrefix}-probe-disabled`} style={{ visibility: "hidden" }}>
          {t("禁用本平台探测")}
        </label>
        <div className="subscription-switch-item">
          <label className="subscription-switch-label" htmlFor={`${idPrefix}-probe-disabled`}>
            <span>{t("禁用本平台探测")}</span>
            <span
              className="subscription-info-icon"
              title={t("开启后，本平台节点不再参与主动健康探测（延迟测速与出口探测）。")}
              aria-label={t("开启后，本平台节点不再参与主动健康探测（延迟测速与出口探测）。")}
              tabIndex={0}
            >
              <Info size={13} />
            </span>
          </label>
          <Switch id={`${idPrefix}-probe-disabled`} {...register("probe_disabled")} />
        </div>
      </div>

      <div className="field-group">
        <label className="field-label" htmlFor={`${idPrefix}-probe-latency-interval`}>
          {t("延迟测速间隔（可选，留空使用全局）")}
        </label>
        <Input
          id={`${idPrefix}-probe-latency-interval`}
          placeholder={t("例如 30m")}
          invalid={Boolean(errors.probe_latency_interval)}
          {...register("probe_latency_interval")}
        />
        {errors.probe_latency_interval?.message ? (
          <p className="field-error">{t(errors.probe_latency_interval.message)}</p>
        ) : null}
      </div>

      <div className="field-group">
        <label className="field-label" htmlFor={`${idPrefix}-probe-egress-interval`}>
          {t("出口探测间隔（可选，留空使用全局）")}
        </label>
        <Input
          id={`${idPrefix}-probe-egress-interval`}
          placeholder={t("例如 24h")}
          invalid={Boolean(errors.probe_egress_interval)}
          {...register("probe_egress_interval")}
        />
        {errors.probe_egress_interval?.message ? (
          <p className="field-error">{t(errors.probe_egress_interval.message)}</p>
        ) : null}
      </div>

      <div className="field-group">
        <label className="field-label" htmlFor={`${idPrefix}-probe-latency-test-url`}>
          {t("延迟测速 URL（可选，留空使用全局）")}
        </label>
        <Input
          id={`${idPrefix}-probe-latency-test-url`}
          placeholder="https://www.gstatic.com/generate_204"
          {...register("probe_latency_test_url")}
        />
      </div>
    </>
  );
}
