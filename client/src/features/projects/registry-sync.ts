import { z } from "zod";

export const registrySyncSchema = z.object({
  enabled: z.boolean(),
  imageRepository: z.string().optional(),
  registryUsername: z.string().optional(),
  registrySecretRef: z.string().optional(),
});

export const registrySyncForm = z.object({
  mode: z.enum(["disabled", "enabled"]),
  repository: z.string().trim(),
  username: z.string().trim(),
  secretRef: z.string(),
});

export function registryFormValues(raw: unknown): z.infer<typeof registrySyncForm> {
  const config = registrySyncSchema.safeParse(raw).data;
  return {
    mode: config?.enabled ? "enabled" : "disabled",
    repository: config?.imageRepository ?? "",
    username: config?.registryUsername ?? "",
    secretRef: config?.registrySecretRef ?? "",
  };
}

export function registryPayload(form: z.infer<typeof registrySyncForm>) {
  if (form.mode === "disabled") return { enabled: false };
  return {
    enabled: true,
    imageRepository: form.repository,
    ...(form.username && { registryUsername: form.username, registrySecretRef: form.secretRef }),
  };
}
