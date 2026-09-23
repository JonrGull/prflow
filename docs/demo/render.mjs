// Render the stage to video, frame by frame, so timing is exact whatever the
// machine's speed.
//
//   node render.mjs timeline.json out.mp4          full video
//   node render.mjs timeline.json stills 5,12.5    PNG stills at those seconds
import { chromium } from 'playwright-core';
import { spawn } from 'child_process';
import fs from 'fs'; import path from 'path';

const [,, tlPath, target, times] = process.argv;
const FPS = 30;
const ffmpegPath = process.env.FFMPEG || 'ffmpeg';
const timeline = JSON.parse(fs.readFileSync(tlPath, 'utf8'));

// CHROME points at any Chromium; without it playwright-core looks for its own.
const browser = await chromium.launch({ executablePath: process.env.CHROME || undefined });
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
await page.goto('file://' + path.resolve('stage.html'));
const total = await page.evaluate(tl => boot(tl), timeline);

if (target === 'stills') {
  fs.mkdirSync('stills', { recursive: true });
  const ts = times.split(',').map(Number).sort((a, b) => a - b);
  for (const t of ts) {
    // Frames must be visited in order: the terminal only ever moves forward.
    for (let f = 0; f / FPS <= t; f++) await page.evaluate(x => frame(x), f / FPS);
    await page.screenshot({ path: `stills/t${t.toFixed(1)}.png` });
  }
} else {
  const ff = spawn(ffmpegPath, ['-y', '-loglevel', 'error', '-f', 'image2pipe', '-framerate', String(FPS), '-i', '-',
    '-c:v', 'libx264', '-preset', 'slow', '-crf', '20', '-tune', 'animation', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', target],
    { stdio: ['pipe', 'inherit', 'inherit'] });
  const frames = Math.ceil(total * FPS);
  for (let f = 0; f < frames; f++) {
    await page.evaluate(x => frame(x), f / FPS);
    const buf = await page.screenshot({ type: 'png' });
    if (!ff.stdin.write(buf)) await new Promise(r => ff.stdin.once('drain', r));
    if (f % 150 === 0) console.log(`frame ${f}/${frames}`);
  }
  ff.stdin.end();
  await new Promise(r => ff.on('close', r));
}
await browser.close();
console.log(`total ${total.toFixed(1)}s`);
