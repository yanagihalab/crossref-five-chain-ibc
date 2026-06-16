import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";

const root = process.cwd();
const htmlPath = path.join(root, "visualizer", "crossref-test-visualizer.html");
const html = fs.readFileSync(htmlPath, "utf8");

class Element {
  constructor(tagName, id = "") {
    this.tagName = tagName;
    this.id = id;
    this.children = [];
    this.attributes = {};
    this.dataset = {};
    this.listeners = {};
    this.textContent = "";
    this.innerHTML = "";
    this.className = "";
    this.type = "";
    this.style = {};
  }

  appendChild(child) {
    this.children.push(child);
    return child;
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }

  getAttribute(name) {
    return this.attributes[name] ?? null;
  }

  addEventListener(name, callback) {
    this.listeners[name] = callback;
  }

  click() {
    if (this.listeners.click) {
      this.listeners.click();
    }
  }
}

class CanvasElement extends Element {
  constructor(id) {
    super("canvas", id);
    this.width = 1100;
    this.height = 420;
    this.context = new CanvasContext();
  }

  getContext(kind) {
    if (kind !== "2d") {
      throw new Error(`unexpected canvas context: ${kind}`);
    }
    return this.context;
  }
}

class CanvasContext {
  constructor() {
    this.ops = [];
    this.lineWidth = 1;
    this.strokeStyle = "";
    this.fillStyle = "";
    this.font = "";
  }

  clearRect(...args) { this.ops.push(["clearRect", ...args]); }
  fillRect(...args) { this.ops.push(["fillRect", ...args]); }
  beginPath() { this.ops.push(["beginPath"]); }
  moveTo(...args) { this.ops.push(["moveTo", ...args]); }
  lineTo(...args) { this.ops.push(["lineTo", ...args]); }
  quadraticCurveTo(...args) { this.ops.push(["quadraticCurveTo", ...args]); }
  closePath() { this.ops.push(["closePath"]); }
  fill() { this.ops.push(["fill"]); }
  stroke() { this.ops.push(["stroke"]); }
  arc(...args) { this.ops.push(["arc", ...args]); }
  fillText(...args) { this.ops.push(["fillText", ...args]); }
  measureText(text) { return { width: String(text).length * 7 }; }
}

const elements = new Map();
function elementForId(id) {
  if (!elements.has(id)) {
    elements.set(id, id === "flow" ? new CanvasElement(id) : new Element("div", id));
  }
  return elements.get(id);
}

const document = {
  getElementById: elementForId,
  createElement: (tagName) => new Element(tagName),
};

const scriptMatch = html.match(/<script>([\s\S]*)<\/script>\s*<\/body>/);
if (!scriptMatch) {
  throw new Error("visualizer script block not found");
}

vm.runInNewContext(scriptMatch[1], {
  document,
  console,
  Math,
  String,
  Array,
  setInterval: () => 1,
  clearInterval: () => {},
});

const list = elementForId("testList");
const canvas = elementForId("flow");
const summaryCount = elementForId("summaryCount").textContent;
const metricPassed = elementForId("metricPassed").textContent;
const metricFailed = elementForId("metricFailed").textContent;
const metricElapsed = elementForId("metricElapsed").textContent;
const output = elementForId("testOutput").textContent;
const stepButton = elementForId("stepBtn");
const resetButton = elementForId("resetBtn");
const stepLabel = elementForId("stepLabel");
const progressFill = elementForId("progressFill");
const timeline = elementForId("timeline");

const visibleTests = list.children.length;
assertEqual(summaryCount, `${visibleTests} / ${visibleTests} PASS`, "summary count");
assertEqual(metricPassed, String(visibleTests), "passed metric");
assertEqual(metricFailed, "0", "failed metric");
assertEqual(metricElapsed, "broadcast OK", "elapsed metric");
assertIncludes(output, "Docker 5-chain broadcast IBC experiment", "docker output");
assertIncludes(output, "route-19 chain-e/channel-3 -> chain-d/channel-3", "route output");

list.children[3].click();
assertEqual(
  elementForId("title").textContent,
  "TestSendCrossReferencePacketUsesClaimedCapability",
  "send button title"
);
assertEqual(list.children[3].getAttribute("aria-pressed"), "true", "send button pressed state");
assertIncludes(elementForId("target").textContent, "SendCrossReferencePacket", "send target");
assertIncludes(elementForId("verified").textContent, "ErrUnauthorizedChannel", "send verified");
assertEqual(stepLabel.textContent, "Step 1 / 3", "initial send step label");
assertEqual(progressFill.style.width, "33.33333333333333%", "initial send progress");

stepButton.click();
assertEqual(stepLabel.textContent, "Step 2 / 3", "stepped send label");
assertEqual(progressFill.style.width, "66.66666666666666%", "stepped send progress");
assertEqual(timeline.children[1].getAttribute("aria-current"), "step", "second timeline state");

resetButton.click();
assertEqual(stepLabel.textContent, "Step 1 / 3", "reset send label");
assertEqual(timeline.children[0].getAttribute("aria-current"), "step", "reset timeline state");

list.children[4].click();
assertEqual(
  elementForId("title").textContent,
  "TestFullAppWiringSkeleton",
  "full app button title"
);
assertIncludes(elementForId("target").textContent, "NewCrossrefSkeleton", "full app target");

list.children[5].click();
assertEqual(
  elementForId("title").textContent,
  "DockerFiveChainIBCExperiment",
  "network button title"
);
assertEqual(stepLabel.textContent, "Step 1 / 20", "network initial step label");
assertIncludes(elementForId("target").textContent, "route-00..route-19", "network target");
stepButton.click();
assertEqual(stepLabel.textContent, "Step 2 / 20", "network stepped label");

if (canvas.context.ops.length < 40) {
  throw new Error(`canvas did not draw enough operations: ${canvas.context.ops.length}`);
}
if (!canvas.context.ops.some((op) => op[0] === "arc")) {
  throw new Error("canvas did not draw animated token arc");
}

console.log(JSON.stringify({
  ok: true,
  visualizer: path.relative(root, htmlPath),
  passed: visibleTests,
  failed: 0,
  elapsed: metricElapsed,
  canvasOps: canvas.context.ops.length,
  animatedToken: true,
}, null, 2));

function assertEqual(actual, expected, label) {
  if (actual !== expected) {
    throw new Error(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

function assertIncludes(actual, expected, label) {
  if (!actual.includes(expected)) {
    throw new Error(`${label}: expected ${JSON.stringify(actual)} to include ${JSON.stringify(expected)}`);
  }
}
