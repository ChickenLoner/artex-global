"use client";

import { CheckIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { NotificationFilter } from "@/lib/types";

// asText / inputType are value helpers local to this file (tightly tied to control rendering), not placed in channel-fields.
import { type FieldDef, type FieldKind, SEVERITY_OPTIONS } from "./channel-fields";

// asText renders any config value into a string usable by an input box.
// config comes from JSON, so a value may be string / number / boolean / array / null;
// here we only care about "can it fit in a text box", and the concrete serialization is handled by buildConfig.
function asText(v: unknown): string {
  if (typeof v === "string") return v;
  if (v === null || v === undefined) return "";
  return String(v);
}

// inputType maps a field type to the input's type attribute.
function inputType(kind: FieldKind): "text" | "password" | "number" {
  if (kind === "password") return "password";
  if (kind === "number") return "number";
  return "text";
}

// ConfigField renders the corresponding control per field definition.
//
// Handling of masked fields is the one subtlety here: the input box **does not show** the masked value itself, only a line of
// "Saved" hint. That way the UI has just one rule -- text in the box was typed by the user,
// and an empty box means an empty value. If "__masked__:…abc123" were put into the input box, the user would think it is placeholder text they have to
// delete themselves, which makes it easier to clear credentials by mistake.
//
// def.label / def.help / def.options[].label are i18n keys of the notifyPage namespace,
// resolved here with t() (see channel-fields.ts).
export function ConfigField({
  def,
  value,
  isSecret,
  onChange,
}: {
  def: FieldDef;
  value: unknown;
  isSecret: boolean;
  onChange: (v: unknown) => void;
}) {
  const t = useTranslations("notifyPage");
  const id = `n-cfg-${def.key}`;
  const raw = asText(value);
  // Masked value echoed by the backend: shaped like "__masked__:…abc123", with a recognizable fragment of the original value at the tail.
  const masked = isSecret && raw.startsWith("__masked__");
  const maskedTail = masked ? (raw.split("…")[1] ?? "") : "";

  if (def.kind === "switch") {
    return (
      <div className="flex items-center gap-2 text-sm">
        <Switch checked={value === true} onCheckedChange={onChange} aria-label={t(def.label)} />
        {t(def.label)}
        {def.help && <span className="text-muted-foreground">({t(def.help)})</span>}
      </div>
    );
  }

  if (def.kind === "select") {
    return (
      <div className="grid gap-2">
        <Label>{t(def.label)}</Label>
        <Select value={raw || def.options?.[0]?.value} onValueChange={onChange}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(def.options ?? []).map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {t(o.label)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    );
  }

  // Controls are dispatched by field type. An if chain is used instead of nested ternaries, because four kinds of controls must be distinguished here,
  // and a three-level ternary already forces you to stop and count parentheses.
  function control() {
    if (def.kind === "textarea" || def.kind === "kv") {
      return (
        <Textarea
          id={id}
          className="font-mono"
          placeholder={def.placeholder}
          value={masked ? "" : raw}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    }
    if (def.kind === "list") {
      return (
        <Input
          id={id}
          value={Array.isArray(value) ? (value as string[]).join(", ") : raw}
          onChange={(e) => onChange(e.target.value)}
          placeholder={def.placeholder}
        />
      );
    }
    return (
      <Input
        id={id}
        className={def.kind === "text" ? "font-mono" : ""}
        type={inputType(def.kind)}
        placeholder={def.placeholder}
        value={masked ? "" : raw}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  const hint = masked ? (
    <p className="text-muted-foreground flex items-center gap-1 text-xs">
      <CheckIcon className="size-3" />
      {maskedTail ? t("masked.savedTail", { tail: maskedTail }) : t("masked.saved")}
    </p>
  ) : (
    def.help && <p className="text-muted-foreground text-xs">{t(def.help)}</p>
  );

  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{t(def.label)}</Label>
      {control()}
      {hint}
    </div>
  );
}

// FilterSummary summarizes the filter conditions into one line, so the card shows what this channel pushes without expanding.
export function FilterSummary({ filter }: { filter: NotificationFilter }) {
  const t = useTranslations("notifyPage");
  const parts: string[] = [];
  if (filter.min_severity) {
    const opt = SEVERITY_OPTIONS.find((o) => o.value === filter.min_severity);
    parts.push(opt ? t(opt.label) : filter.min_severity);
  }
  if (filter.vulnclass_include?.length) parts.push(t("summary.includeCount", { n: filter.vulnclass_include.length }));
  if (filter.vulnclass_exclude?.length) parts.push(t("summary.excludeCount", { n: filter.vulnclass_exclude.length }));
  if (filter.task_ids?.length) parts.push(t("summary.taskCount", { n: filter.task_ids.length }));
  if (filter.asset_ids?.length) parts.push(t("summary.assetCount", { n: filter.asset_ids.length }));
  if (filter.on_status_change) parts.push(t("summary.statusChange"));
  if (parts.length === 0) {
    return <p className="text-muted-foreground text-sm">{t("summary.all")}</p>;
  }
  return <p className="text-muted-foreground text-sm">{parts.join(" · ")}</p>;
}
