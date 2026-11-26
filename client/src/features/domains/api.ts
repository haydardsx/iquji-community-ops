import type {
  Certificate,
  Domain,
  DomainRedirect,
  Ingress,
  OperationRun,
  Page,
  Resolver,
  Server,
} from "@/contracts/types";
import { fetchJson } from "@/shared/api/client";

export function fetchDomains() {
  return fetchJson<{ items: Domain[] }>("secrets/domains");
}

export type DomainInput = {
  name: string;
  certificateId?: string;
  parentDomainId?: string;
};

export type CertificateInput = {
  name: string;
  domains: string[];
  issuer: "lets_encrypt";
  challengeType: "http-01";
  /** 新建证书时直接选择网关服务器，签发入口由后端按网关集合派生。 */
  serverIds?: string[];
  resolverId?: string;
};

export function createDomain(input: DomainInput) {
  return fetchJson<Domain>("secrets/domains", { method: "POST", body: input });
}

export function updateDomain(id: string, input: DomainInput) {
  return fetchJson<Domain>(`secrets/domains/${id}`, { method: "PUT", body: input });
}

export function deleteDomain(domainId: string) {
  return fetchJson<{ ok: boolean }>(`secrets/domains/${domainId}`, { method: "DELETE" });
}

export function saveDomainRedirect(id: string, input: DomainRedirect) {
  return fetchJson<Domain>(`secrets/domains/${id}/redirect`, { method: "PUT", body: input });
}

export function runDomainRedirect(id: string, action: "apply" | "remove", requestKey: string) {
  return fetchJson<OperationRun>(`secrets/domains/${id}/redirect/${action}`, {
    method: "POST",
    requestKey,
  });
}

export function fetchCertificates() {
  return fetchJson<{ items: Certificate[] }>("secrets/certificates");
}

export function createCertificate(input: CertificateInput) {
  return fetchJson<{ certificate: Certificate }>("secrets/certificates", {
    method: "POST",
    body: input,
  });
}

export function updateCertificate(id: string, input: CertificateInput) {
  return fetchJson<Certificate>(`secrets/certificates/${id}`, { method: "PUT", body: input });
}

export function deleteCertificate(certificateId: string) {
  return fetchJson<{ ok: boolean }>(`secrets/certificates/${certificateId}`, {
    method: "DELETE",
  });
}

/** 发起真实 ACME 签发校验；创建后必须确认才会进入 Worker 执行。 */
export function verifyCertificate(certificateId: string, requestKey?: string) {
  return fetchJson<OperationRun>(`secrets/certificates/${certificateId}/verify`, {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
  });
}

export function fetchResolvers() {
  return fetchJson<{ items: Resolver[] }>("secrets/resolvers");
}

export function fetchIngresses() {
  return fetchJson<{ items: Ingress[] }>("secrets/ingresses");
}

export function fetchDomainServers() {
  return fetchJson<Page<Server>>("servers");
}
