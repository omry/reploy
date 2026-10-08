const { chromium } = require("playwright");

async function main() {
  const [url, screenshot] = process.argv.slice(2);
  if (!url || !screenshot) {
    throw new Error("usage: browser-proof URL SCREENSHOT");
  }
  const browser = await chromium.launch({
    headless: true,
    args: ["--disable-dev-shm-usage", "--no-sandbox"],
  });
  try {
    const page = await browser.newPage({ viewport: { width: 800, height: 600 } });
    let lastError;
    for (let attempt = 0; attempt < 40; attempt += 1) {
      try {
        await page.goto(url, { waitUntil: "networkidle", timeout: 1000 });
        lastError = undefined;
        break;
      } catch (error) {
        lastError = error;
        await new Promise((resolve) => setTimeout(resolve, 250));
      }
    }
    if (lastError) {
      throw lastError;
    }
    const text = await page.locator("#proof").textContent();
    if (text !== "browser-proof") {
      throw new Error(`unexpected endpoint content: ${JSON.stringify(text)}`);
    }
    await page.screenshot({ path: screenshot });
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  console.error(error.stack || String(error));
  process.exit(1);
});
