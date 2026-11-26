import {
  IqujiButton,
  IqujiField,
  IqujiInput,
  IqujiMultiSelect,
  IqujiSelect,
} from "@iquji-community/web-component";
import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { Certificate, Domain, Server } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { FieldHelp, FieldWithHelp } from "@/shared/ui/field-help";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";
import {
  type CertificateInput,
  createCertificate,
  createDomain,
  updateCertificate,
  updateDomain,
} from "./api";

export function DomainDialog({
  open,
  domain,
  domains,
  certificates,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  domain?: Domain | null;
  domains: Domain[];
  certificates: Certificate[];
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState("");
  const [certificateId, setCertificateId] = useState("");
  const [parentDomainId, setParentDomainId] = useState("");
  const notify = useOperationNotice();
  const { t } = useI18n();

  useEffect(() => {
    if (!open) return;
    setName(domain?.name ?? "");
    setCertificateId(domain?.certificateId ?? "");
    setParentDomainId(domain?.parentDomainId ?? "");
  }, [open, domain]);

  const mutation = useMutation({
    mutationFn: () => {
      const input = {
        name: name.trim().toLowerCase(),
        certificateId,
        parentDomainId: parentDomainId || undefined,
      };
      return domain ? updateDomain(domain.id, input) : createDomain(input);
    },
    onSuccess: () => {
      notify(t("domains.saved"));
      onSaved();
      onOpenChange(false);
    },
  });
  useOperationError(mutation.error, t("domains.saveFailed"));
  const parentOptions = domains
    .filter((item) => item.id !== domain?.id)
    .map((item) => ({ label: item.name, value: item.id }));

  return (
    <InspectorDrawer
      description={t("domains.entityDesc")}
      footer={
        <>
          <IqujiButton
            disabled={mutation.isPending}
            onClick={() => onOpenChange(false)}
            variant="secondary"
          >
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            disabled={name.trim().length === 0 || mutation.isPending}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t("common.save")}
          </IqujiButton>
        </>
      }
      onClose={() => {
        if (!mutation.isPending) onOpenChange(false);
      }}
      open={open}
      size="md"
      title={domain ? t("domains.editDomain") : t("domains.createDomain")}
    >
      <div className="ops-form-stack">
        <IqujiField label={t("domains.fieldDomain")} required>
          <IqujiInput
            autoFocus
            onChange={(event) => setName(event.currentTarget.value)}
            placeholder="app.example.com"
            value={name}
          />
        </IqujiField>
        <FieldWithHelp label={t("domains.fieldCert")} help={t("domains.fieldCertDesc")}>
          <IqujiSelect
            onValueChange={(value) => {
              const next = value === "none" ? "" : (value ?? "");
              setCertificateId(next);
            }}
            options={[
              { label: t("domains.noCertHttp"), value: "none" },
              ...certificates.map((item) => ({
                label: `${item.name} · ${(item.domains ?? []).join("、")}`,
                value: item.id,
              })),
            ]}
            value={certificateId || "none"}
          />
        </FieldWithHelp>
        {parentOptions.length > 0 ? (
          <FieldWithHelp label={t("domains.fieldParent")} help={t("domains.fieldParentDesc")}>
            <IqujiSelect
              onValueChange={(value) => setParentDomainId(value === "none" ? "" : (value ?? ""))}
              options={[{ label: t("domains.parentNone"), value: "none" }, ...parentOptions]}
              value={parentDomainId || "none"}
            />
          </FieldWithHelp>
        ) : null}
      </div>
    </InspectorDrawer>
  );
}

export function CertificateDialog({
  open,
  certificate,
  domains,
  servers,
  initialDomainId,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  certificate?: Certificate | null;
  domains: Domain[];
  servers: Server[];
  initialDomainId?: string;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState("");
  const [domainIds, setDomainIds] = useState<string[]>([]);
  const [serverIds, setServerIds] = useState<string[]>([]);
  const notify = useOperationNotice();
  const { t } = useI18n();

  useEffect(() => {
    if (!open) return;
    setName(certificate?.name ?? "");
    if (certificate) {
      setDomainIds(
        (certificate.domains ?? [])
          .map((host) => domains.find((item) => item.name === host)?.id)
          .filter((id): id is string => Boolean(id)),
      );
    } else if (initialDomainId && domains.some((d) => d.id === initialDomainId)) {
      setDomainIds([initialDomainId]);
    } else {
      setDomainIds([]);
    }
    setServerIds(servers.length > 0 ? [servers[0].id] : []);
  }, [open, certificate, domains, servers, initialDomainId]);

  const mutation = useMutation({
    mutationFn: () => {
      const hosts = domainIds
        .map((id) => domains.find((item) => item.id === id)?.name)
        .filter((host): host is string => Boolean(host));
      const input: CertificateInput = {
        name: name.trim() || (hosts[0] ?? t("domains.fieldCert")),
        domains: hosts,
        issuer: "lets_encrypt",
        challengeType: "http-01",
        // 编辑时不改网关：签发入口由后端按现有网关集合派生并复用。
        ...(certificate ? {} : { serverIds }),
      };
      return certificate
        ? updateCertificate(certificate.id, input).then((value) => ({ certificate: value }))
        : createCertificate(input);
    },
    onSuccess: () => {
      notify(t("domains.certSaved"));
      onSaved();
      onOpenChange(false);
    },
  });
  useOperationError(mutation.error, t("domains.certSaveFailed"));
  const canSubmit =
    domainIds.length > 0 && !mutation.isPending && (Boolean(certificate) || serverIds.length > 0);

  return (
    <InspectorDrawer
      description={t("domains.certSectionDesc")}
      headerToolbar={
        <FieldHelp
          label={certificate ? t("domains.editCert") : t("domains.applyCert")}
          help={[t("domains.certNotice"), certificate && t("domains.gatewayHint")]
            .filter(Boolean)
            .join(" ")}
        />
      }
      footer={
        <>
          <IqujiButton
            disabled={mutation.isPending}
            onClick={() => onOpenChange(false)}
            variant="secondary"
          >
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            disabled={!canSubmit}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t("common.save")}
          </IqujiButton>
        </>
      }
      onClose={() => {
        if (!mutation.isPending) onOpenChange(false);
      }}
      open={open}
      size="md"
      title={certificate ? t("domains.editCert") : t("domains.applyCert")}
    >
      <div className="ops-form-stack">
        <IqujiField label={t("domains.certNameField")}>
          <IqujiInput
            autoFocus
            onChange={(event) => setName(event.currentTarget.value)}
            placeholder={t("domains.certNamePlaceholder")}
            value={name}
          />
        </IqujiField>
        <IqujiMultiSelect
          clearLabel={t("domains.clear")}
          label={t("domains.domainsForHttps")}
          onChange={setDomainIds}
          options={domains.map((item) => ({ label: item.name, value: item.id }))}
          placeholder={t("domains.selectDomains")}
          searchable
          value={domainIds}
        />
        {domains.length === 0 ? (
          <p className="ops-muted" role="status">
            {t("domains.noDomainsHint")}
          </p>
        ) : null}
        {!certificate && (
          <IqujiMultiSelect
            clearLabel={t("domains.clear")}
            label={t("domains.validateGateway")}
            onChange={setServerIds}
            options={servers.map((item) => ({ label: item.name, value: item.id }))}
            placeholder={t("domains.selectTraefikServer")}
            searchable
            value={serverIds}
          />
        )}
      </div>
    </InspectorDrawer>
  );
}
