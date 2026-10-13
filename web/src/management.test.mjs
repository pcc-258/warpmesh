import test from "node:test";
import assert from "node:assert/strict";
import { optionalDate } from "./api.ts";
import { enrollmentCommands, expiryPatch, filterDevices, quoteArgument } from "./managementHelpers.ts";

test("device search tolerates missing LAN arrays and combines filters", () => {
 const devices = [{ id: "a", name: "Home desktop", hostname: "pc", os: "linux", group: "Home", online: true }, { id: "b", name: "Work", os: "windows", lanIPs: ["10.0.0.2"], online: false }];
 assert.deepEqual(filterDevices(devices, "home pc", "online", "Home", "linux").map((d) => d.id), ["a"]);
 assert.deepEqual(filterDevices(devices, "10.0.0.2", "offline", "", "").map((d) => d.id), ["b"]);
 assert.equal(filterDevices(devices, "", "online", "", "windows").length, 0);
});
test("clearing expiry emits JSON null rather than an invalid timestamp", () => {
 assert.equal(expiryPatch(""), null);
 assert.equal(JSON.stringify({ expiresAt: expiryPatch("") }), '{"expiresAt":null}');
 assert.ok(Number.isFinite(Date.parse(expiryPatch("2030-12-31"))));
 assert.throws(() => expiryPatch("invalid"));
});
test("enrollment commands use deployment origin and quote shell metacharacters", () => {
 const unix = enrollmentCommands("https://custom.example:8443", "invite", "a'$(touch unsafe)", "linux-arm64");
 assert.ok(unix.includes("'https://custom.example:8443'"));
 assert.ok(unix.includes(quoteArgument("a'$(touch unsafe)")));
 assert.ok(!unix.includes("warpmesh.ddns.net"));
 assert.equal((unix.match(/-data-dir/g) ?? []).length, 2);
 const windows = enrollmentCommands("https://custom.example", "invite", "O'Brien", "windows-amd64");
 assert.ok(windows.includes(".\\warpmesh-agent.exe"));
 assert.ok(windows.includes("'O''Brien'"));
});

test("Go zero timestamps display as no expiry while real dates are preserved", () => {
 assert.equal(optionalDate("0001-01-01T00:00:00Z"), undefined);
 assert.equal(optionalDate(null), undefined);
 assert.equal(optionalDate("2030-12-31T00:00:00Z"), "2030-12-31T00:00:00Z");
});
