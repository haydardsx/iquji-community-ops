import { IqujiOverlayProvider } from "@iquji-community/web-component";
import { useForm } from "react-hook-form";
import { describe, expect, it } from "vitest";
import type { ApplicationDeploymentConfig, ApplicationRuntime } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ApplicationDirectPorts } from "./ApplicationDirectPorts";
import { type ApplicationForm, applicationFormSchema, configPayload, formValues } from "./model";

const config: ApplicationDeploymentConfig = {
  deploymentManagerServerId: "manager",
  sourceConfig: { provider: "image", image: "example/service:1" },
  swarmRuntime: {
    ports: [{ name: "web", protocol: "http", targetPort: 3000, publishedPort: 8080 }],
    services: [{ slotKey: "main", serviceName: "example", serverId: "worker" }],
  },
};

function DirectPorts({ runtime }: { runtime: ApplicationRuntime }) {
  const form = useForm<ApplicationForm>({ defaultValues: formValues(config) });
  return (
    <ApplicationDirectPorts
      form={form}
      disabled={false}
      portRows={[{ id: "row-web" }]}
      runtime={runtime}
      error={null}
      loading={false}
    />
  );
}

describe("应用访问入口", () => {
  it("无端口服务的保存不会生成默认 HTTP 入口", () => {
    const input = { ...config, swarmRuntime: { ...config.swarmRuntime, ports: [] } };
    const saved = configPayload(applicationFormSchema.parse(formValues(input)), input);
    expect(saved.swarmRuntime?.ports).toEqual([]);
    expect(saved.swarmRuntime?.containerPort).toBeUndefined();
    expect(saved.swarmRuntime?.publishedPort).toBeUndefined();
  });
  it("部署无需 HTTP 网关，旧版每槽位的映射在修改时独立保留", () => {
    const old = {
      ...config,
      swarmRuntime: {
        stackName: "old",
        containerPort: 80,
        publishedPort: 8000,
        services: [
          { slotKey: "web", serviceName: "web-1", publishedPort: 8100 },
          { slotKey: "admin", serviceName: "web-2", publishedPort: 8200 },
        ],
      },
    };
    const form = formValues(old);
    form.services[1].publishedPort = 8300;
    const saved = configPayload(applicationFormSchema.parse(form), old);
    expect(saved).toMatchObject({
      deploymentManagerServerId: "manager",
      gatewayServerId: "",
      swarmRuntime: {
        publishedPort: 8000,
        services: [{ publishedPort: 8100 }, { publishedPort: 8300 }],
      },
    });
    expect(saved.swarmRuntime).not.toHaveProperty("stackName");
  });

  it("复用容器的端口声明，只有确认可达时才显示直连地址", () => {
    const runtime: ApplicationRuntime = {
      availability: "available",
      status: "running",
      checkedAt: "2026-01-01T00:00:00Z",
      services: [
        {
          serviceSlotKey: "main",
          serviceId: "svc-demo",
          serviceName: "example",
          serverId: "worker",
          runningReplicas: 1,
          desiredReplicas: 1,
          expectedReplicas: 1,
          tasks: [],
          ports: [
            {
              name: "web",
              protocol: "http",
              containerPort: 3000,
              publishedPort: 8080,
              publishedHost: "2001:db8::1",
              publishedVerified: true,
            },
            {
              name: "pending",
              protocol: "tcp",
              containerPort: 3001,
              publishedPort: 8081,
              publishedHost: "pending.example.test",
            },
          ],
        },
      ],
    };
    const markup = renderToStaticMarkup(
      <IqujiOverlayProvider>
        <DirectPorts runtime={runtime} />
      </IqujiOverlayProvider>,
    );
    expect(markup).toContain('name="ports.0.publishedPort"');
    expect(markup).not.toContain('name="ports.0.targetPort"');
    expect(markup).toContain("[2001:db8::1]:8080");
    expect(markup).toContain("发布端口 8081，尚未验证可达性");
    expect(markup).not.toContain("pending.example.test");
  });
});
