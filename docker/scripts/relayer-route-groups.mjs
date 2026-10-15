export function groupRoutesByChannelPair(routes) {
  const groups = new Map();
  for (const route of routes) {
    const endpoints = [
      [route.fromChainId, route.fromChannel],
      [route.toChainId, route.toChannel],
    ].sort(([leftChain, leftChannel], [rightChain, rightChannel]) =>
      leftChain.localeCompare(rightChain) || leftChannel.localeCompare(rightChannel),
    );
    const key = JSON.stringify(endpoints);
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(route);
  }
  return [...groups.values()];
}
