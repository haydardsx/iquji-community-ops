// 使用 RFC 1918 和 RFC 4193 网段的起始地址测试私网边界，无需实际主机。
export const [privateIPv4Boundary] = "172.16.0.0/12".split("/");
export const [privateIPv6Boundary] = "fc00::/7".split("/");

// 取给定 IPv4 前缀内的第一个和最后一个可用主机地址。
export function hostRange(cidr: string): [string, string] {
  const [network, bits] = cidr.split("/");
  const start = network.split(".").reduce((total, octet) => total * 256 + Number(octet), 0);
  const end = start + 2 ** (32 - Number(bits)) - 1;
  const address = (value: number) =>
    [24, 16, 8, 0].map((shift) => (value >>> shift) & 255).join(".");
  return [address(start + 1), address(end - 1)];
}
