import type { Device } from "./api";

export function filterDevices(devices: Device[], query: string, status: string, group: string, os: string) {
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  return devices.filter((device) => {
    const text = [device.name, device.hostname, device.id, device.group, device.os, ...(device.lanIPs ?? [])].join(" ").toLowerCase();
    return terms.every((term) => text.includes(term)) &&
      (status === "all" || device.online === (status === "online")) &&
      (!group || device.group === group) && (!os || device.os === os);
  });
}

export function expiryPatch(value: string): string | null {
  if (!value) return null;
  const date = new Date(`${value}T23:59:59`);
  if (!Number.isFinite(date.getTime())) throw new Error("Choose a valid expiry date.");
  return date.toISOString();
}

export function quoteArgument(value: string, windows = false): string {
  return windows ? `'${value.replaceAll("'", "''")}'` : `'${value.replaceAll("'", "'\\''")}'`;
}

export function enrollmentCommands(origin: string, code: string, name: string, platform: string) {
  const windows = platform.startsWith("windows");
  const quote = (value: string) => quoteArgument(value, windows);
  const binary = windows ? "warpmesh-agent.exe" : "warpmesh-agent";
  // Binary download uses the signed-in browser, not a cookie-less curl process.
  const enroll = `.\\${binary} enroll -server ${quote(origin)} -code ${quote(code)} -name ${quote(name)} -data-dir .\\warpmesh-data`;
  if (windows) return `${enroll}\n.\\${binary} -server ${quote(origin)} -data-dir .\\warpmesh-data`;
  return `chmod +x ./${binary}\n./${binary} enroll -server ${quote(origin)} -code ${quote(code)} -name ${quote(name)} -data-dir ./warpmesh-data\n./${binary} -server ${quote(origin)} -data-dir ./warpmesh-data`;
}
