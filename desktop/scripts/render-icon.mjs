#!/usr/bin/env node
/**
 * Rasterize brand/logo-mark.svg → app-icon.png + public splash PNGs.
 * Requires: npm install (devDependency @resvg/resvg-js)
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Resvg } from "@resvg/resvg-js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const desktop = path.resolve(__dirname, "..");
const svgPath = path.join(desktop, "brand", "logo-mark.svg");
const svg = fs.readFileSync(svgPath);

function render(size, out) {
  const png = new Resvg(svg, { fitTo: { mode: "width", value: size } }).render().asPng();
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, png);
  console.log("wrote", out, png.length, "bytes");
}

render(1024, path.join(desktop, "app-icon.png"));
render(256, path.join(desktop, "public", "logo-mark.png"));
