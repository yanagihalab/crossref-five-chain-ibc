import assert from "node:assert/strict";

export const RELAYER_STRATEGIES = ["overlap", "static", "cooperative", "adaptive", "tx-pool"];
export const STATIC_PAIRS = [["chain-a", "chain-b"], ["chain-a", "chain-d"], ["chain-b", "chain-d"]];

export function validateRelayerStrategy(strategy = "static") {
  assert.ok(RELAYER_STRATEGIES.includes(strategy), "Unknown relayer strategy");
  return strategy;
}

export function channelGroups(inv, actual) {
  assert.equal(actual.channelsVerified, true, "Channel handshakes must be verified before assignment");
  assert.equal(actual.pairs.length, 3);
  const used = new Set();
  const groups = STATIC_PAIRS.map(domains => {
    const pairs = actual.pairs.filter(p => p.left !== p.right && domains.includes(p.left) && domains.includes(p.right));
    assert.equal(pairs.length, 1, "Expected one channel pair per chain pair");
    const pair = pairs[0];
    assert.equal(pair.routes.length, 2, "Both directions must stay in the same assignment group");
    const channels = {};
    for (const [domain, channel] of [[pair.left, pair.leftChannel], [pair.right, pair.rightChannel]]) {
      assert.match(channel, /^channel-\d{1,6}$/);
      const chain = inv.chains.find(c => c.domain === domain);
      assert.ok(chain, "Unknown assignment chain");
      assert.ok(!used.has(`${chain.id}/${channel}`), "Channel endpoint belongs to multiple groups");
      used.add(`${chain.id}/${channel}`);
      channels[chain.id] = channel;
    }
    return { id: domains.join("/"), domains, channels };
  });
  return groups;
}

export function planRelayerAssignments(inv, actual, options = {}) {
  const strategy = validateRelayerStrategy(options.strategy);
  const groups = channelGroups(inv, actual);
  const names = ["relayer-1", "relayer-2", "relayer-3"];
  const owners = Object.fromEntries(groups.map((g, i) => [g.id, ["overlap", "tx-pool"].includes(strategy) ? names : [names[i]]]));
  let retained = false;
  if (strategy === "adaptive") {
    const now = options.now ?? Date.now(), maxAgeMs = options.maxAgeMs ?? 15000;
    assert.ok(Number.isFinite(now) && Number.isFinite(maxAgeMs) && maxAgeMs > 0);
    const minimumImprovement = options.minimumImprovement ?? 0.2;
    const cooldownMs = options.cooldownMs ?? 60000;
    assert.ok(Number.isFinite(minimumImprovement) && minimumImprovement >= 0 && minimumImprovement < 1);
    assert.ok(Number.isFinite(cooldownMs) && cooldownMs >= 0);
    assert.ok(options.lastChangedAt === undefined || (Number.isFinite(options.lastChangedAt) && options.lastChangedAt <= now));
    const metrics = options.metrics;
    assert.ok(metrics && Number.isFinite(metrics.observedAt), "Adaptive assignment requires timestamped measurements");
    assert.ok(metrics.observedAt <= now && now - metrics.observedAt <= maxAgeMs, "Adaptive measurements are stale");
    const workers = names.map(name => {
      const m = metrics.workers?.[name];
      assert.ok(m && typeof m.healthy === "boolean", "Worker health must be measured explicitly");
      assert.ok(Number.isFinite(m.capacity) && m.capacity > 0, "Measured worker capacity must be positive");
      assert.ok(m.availableDiskBytes === undefined || (Number.isFinite(m.availableDiskBytes) && m.availableDiskBytes >= 0));
      return { ...m, name };
    }).filter(m => m.healthy && (m.availableDiskBytes === undefined || m.availableDiskBytes >= 3 * 2 ** 30));
    assert.ok(workers.length, "No healthy relayer remains; pause without relay attempts");
    const weights = Object.fromEntries(groups.map(g => {
      const weight = metrics.groups?.[g.id]?.weight;
      assert.ok(Number.isFinite(weight) && weight > 0, "Every channel group needs a measured positive demand weight");
      return [g.id, weight];
    }));
    const loads = Object.fromEntries(workers.map(w => [w.name, 0]));
    const counts = Object.fromEntries(workers.map(w => [w.name, 0]));
    for (const group of [...groups].sort((a, b) => weights[b.id] - weights[a.id] || a.id.localeCompare(b.id))) {
      const worker = [...workers].sort((a, b) =>
        (loads[a.name] + weights[group.id]) / a.capacity - (loads[b.name] + weights[group.id]) / b.capacity ||
        counts[a.name] - counts[b.name] || a.name.localeCompare(b.name))[0];
      owners[group.id] = [worker.name];
      loads[worker.name] += weights[group.id];
      counts[worker.name]++;
    }
    const previous = options.previous;
    const validPrevious = previous?.strategy === strategy && JSON.stringify(previous.groups) === JSON.stringify(groups) && groups.every(g =>
      previous.owners?.[g.id]?.length === 1 && workers.some(w => w.name === previous.owners[g.id][0]));
    if (validPrevious) {
      const peak = assignment => Math.max(...workers.map(w => groups
        .filter(g => assignment[g.id][0] === w.name).reduce((sum, g) => sum + weights[g.id], 0) / w.capacity));
      const oldPeak = peak(previous.owners), newPeak = peak(owners);
      if (now - (options.lastChangedAt ?? -Infinity) < cooldownMs || newPeak >= oldPeak * (1 - minimumImprovement)) {
        for (const group of groups) owners[group.id] = [...previous.owners[group.id]];
        retained = true;
      }
    }
  }
  const workers = names.map(name => {
    const channels = {};
    for (const group of groups.filter(g => owners[g.id].includes(name))) {
      for (const [chain, channel] of Object.entries(group.channels)) (channels[chain] ??= []).push(channel);
    }
    for (const list of Object.values(channels)) list.sort();
    return { name, channels };
  });
  return { strategy, groups, owners, workers, retained };
}

export function sameRelayerAssignments(a, b) {
  return Boolean(a && b && a.strategy === b.strategy && JSON.stringify(a.workers) === JSON.stringify(b.workers));
}

export function validateExclusiveAssignments(plan) {
  assert.ok(["cooperative", "adaptive"].includes(plan.strategy));
  assert.ok(Array.isArray(plan.groups) && plan.groups.length > 0);
  const names = ["relayer-1", "relayer-2", "relayer-3"];
  assert.deepEqual(plan.workers.map(w => w.name).sort(), names);
  assert.deepEqual(Object.keys(plan.owners).sort(), plan.groups.map(g => g.id).sort());
  const expected = Object.fromEntries(names.map(name => [name, {}])), used = new Set();
  for (const g of plan.groups) {
    assert.equal(Object.keys(g.channels).length, 2, "Never split a channel pair into directed groups");
    const owners = plan.owners[g.id];
    assert.ok(Array.isArray(owners) && owners.length === 1 && names.includes(owners[0]));
    for (const [chain, channel] of Object.entries(g.channels)) {
      assert.match(channel, /^channel-\d{1,6}$/);
      assert.ok(!used.has(`${chain}/${channel}`), "Duplicate assignment endpoint");
      used.add(`${chain}/${channel}`);
      (expected[owners[0]][chain] ??= []).push(channel);
    }
  }
  const sorted = channels => Object.fromEntries(Object.entries(channels).sort(([a], [b]) => a.localeCompare(b))
    .map(([chain, values]) => { assert.ok(Array.isArray(values)); return [chain, [...values].sort()]; }));
  for (const worker of plan.workers) assert.deepEqual(sorted(worker.channels), sorted(expected[worker.name]), "Filters must exactly match exclusive channel-group ownership");
}

export function cooperativeAssignments(inv, actual, states, previous) {
  const initial = planRelayerAssignments(inv, actual, { strategy: "cooperative" });
  const names = initial.workers.map(w => w.name);
  assert.deepEqual(Object.keys(states).sort(), [...names].sort(), "All workers need fresh status");
  const healthy = names.filter(name => {
    const s = states[name];
    assert.ok(s && typeof s.healthy === "boolean", "Missing explicit worker health");
    return s.healthy;
  });
  assert.ok(healthy.length, "No healthy relayer remains");
  if (previous) {
    validateExclusiveAssignments(previous);
    assert.equal(previous.strategy, "cooperative");
    assert.deepEqual(previous.groups, initial.groups);
  }
  const owners = {}, load = Object.fromEntries(healthy.map(name => [name, 0]));
  for (const group of initial.groups) {
    const owner = (previous ?? initial).owners[group.id][0];
    if (healthy.includes(owner)) { owners[group.id] = [owner]; load[owner]++; }
  }
  for (const group of initial.groups.filter(g => !owners[g.id])) {
    const owner = [...healthy].sort((a, b) => load[a] - load[b] || a.localeCompare(b))[0];
    owners[group.id] = [owner]; load[owner]++;
  }
  const workers = names.map(name => {
    const channels = {};
    for (const group of initial.groups.filter(g => owners[g.id][0] === name))
      for (const [chain, channel] of Object.entries(group.channels)) (channels[chain] ??= []).push(channel);
    for (const list of Object.values(channels)) list.sort();
    return { name, channels };
  });
  const plan = { ...initial, owners, workers };
  validateExclusiveAssignments(plan);
  return plan;
}
