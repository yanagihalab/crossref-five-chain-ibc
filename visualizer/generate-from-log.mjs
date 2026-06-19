#!/usr/bin/env node
import fs from "node:fs";

const logPath = process.argv[2];
if (!logPath) {
  console.error("usage: node visualizer/generate-from-log.mjs <experiment.log> [output.json]");
  process.exit(2);
}

const outputPath = process.argv[3] ?? "visualizer/test-results.json";
const log = fs.readFileSync(logPath, "utf8");
const routePattern = /(route-\d+)\s+(chain-[a-z0-9]+)\/(channel-\d+)\s+->\s+(chain-[a-z0-9]+)\/(channel-\d+)/g;
const routes = [];
let match;
while ((match = routePattern.exec(log)) !== null) {
  routes.push({
    route_id: match[1],
    source_domain: match[2],
    source_channel: match[3],
    destination_domain: match[4],
    destination_channel: match[5],
  });
}

const storedPattern = /(route-\d+)\s+(chain-[a-z0-9]+)\s+stored cross-reference for\s+(chain-[a-z0-9]+)/g;
const stored = [];
while ((match = storedPattern.exec(log)) !== null) {
  stored.push({
    route_id: match[1],
    local_domain: match[2],
    remote_domain: match[3],
  });
}

const accountabilityEvents = [];
const eventPattern = /accountability(?: event)?:?\s+(\{[^\n]+\})/gi;
while ((match = eventPattern.exec(log)) !== null) {
  try {
    accountabilityEvents.push(JSON.parse(match[1]));
  } catch {
    accountabilityEvents.push({ raw: match[1] });
  }
}

const controllerMetrics = [];
const metricPattern = /relayer-controller(?: metric)?:?\s+(\{[^\n]+\})/gi;
while ((match = metricPattern.exec(log)) !== null) {
  try {
    controllerMetrics.push(JSON.parse(match[1]));
  } catch {
    controllerMetrics.push({ raw: match[1] });
  }
}

const result = {
  generated_from: logPath,
  ok: /cross-reference experiment passed/i.test(log),
  summary: {
    routes: routes.length,
    stored_cross_references: stored.length,
    has_ics23_proof: /source_checkpoint_proof|ICS23|membership proof/i.test(log),
    has_hysteresis_signature: /hysteresis_signature|hysteresis public key/i.test(log),
    accountability_events: accountabilityEvents.length,
    controller_metrics: controllerMetrics.length,
  },
  accountability_events: accountabilityEvents,
  controller_metrics: controllerMetrics,
  tests: [
    {
      name: "DockerCrossrefExperiment",
      status: /cross-reference experiment passed/i.test(log) ? "PASS" : "UNKNOWN",
      routes,
      stored,
    },
  ],
};

fs.writeFileSync(outputPath, `${JSON.stringify(result, null, 2)}\n`);
console.log(JSON.stringify({ output: outputPath, ...result.summary, ok: result.ok }, null, 2));
