import puppeteer from 'puppeteer-core';

const LIGHTPANDA_WS = 'ws://127.0.0.1:9222';

async function testLightpanda() {
  console.log('🚀 Test Lightpanda Browser\n');

  let browser;
  try {
    browser = await puppeteer.connect({
      browserWSEndpoint: LIGHTPANDA_WS,
    });
    console.log('✅ Connesso a Lightpanda via CDP\n');
  } catch (err) {
    console.error('❌ Connessione fallita:', err.message);
    return;
  }

  try {
    // Test 1: Fetch pagina semplice con dump HTML
    console.log('═'.repeat(50));
    console.log('📄 TEST 1: Fetch HTML da URL');
    console.log('═'.repeat(50));
    const context1 = await browser.createBrowserContext();
    const page1 = await context1.newPage();

    await page1.goto('https://en.wikipedia.org/wiki/Web_scraping', {
      waitUntil: 'load',
      timeout: 30000,
    });

    const title = await page1.title();
    console.log(`✅ Titolo pagina: "${title}"`);

    const url = page1.url();
    console.log(`✅ URL: ${url}`);

    // Estrai link dalla pagina
    const links = await page1.evaluate(() => {
      return Array.from(document.querySelectorAll('a[href]'))
        .map(a => a.href)
        .filter(h => h.startsWith('http'))
        .slice(0, 5);
    });
    console.log(`✅ Trovati ${links.length} link:`);
    links.forEach((l, i) => console.log(`   ${i+1}. ${l}`));

    await context1.close();
    console.log('');

    // Test 2: Pagina con video
    console.log('═'.repeat(50));
    console.log('📄 TEST 2: Pagina HTML con elementi multimediali');
    console.log('═'.repeat(50));
    const context2 = await browser.createBrowserContext();
    const page2 = await context2.newPage();

    await page2.goto('https://www.w3schools.com/html/html5_video.asp', {
      waitUntil: 'load',
      timeout: 30000,
    });

    const title2 = await page2.title();
    console.log(`✅ Titolo: "${title2}"`);

    // Cerca elementi video e immagini
    const media = await page2.evaluate(() => {
      const videos = document.querySelectorAll('video');
      const images = document.querySelectorAll('img');
      return {
        videoCount: videos.length,
        imageCount: images.length,
        firstImages: Array.from(images).slice(0, 3).map(img => img.src || img.getAttribute('src')),
      };
    });
    console.log(`✅ Video: ${media.videoCount}, Immagini: ${media.imageCount}`);
    if (media.firstImages.length > 0) {
      console.log(`✅ Prime immagini:`);
      media.firstImages.forEach((src, i) => console.log(`   ${i+1}. ${src.substring(0, 80)}...`));
    }

    await context2.close();
    console.log('');

    // Test 3: Esecuzione JavaScript
    console.log('═'.repeat(50));
    console.log('📄 TEST 3: Esecuzione JavaScript e DOM');
    console.log('═'.repeat(50));
    const context3 = await browser.createBrowserContext();
    const page3 = await context3.newPage();

    await page3.goto('https://example.com', {
      waitUntil: 'load',
      timeout: 30000,
    });

    // Estrai informazioni dal DOM
    const pageInfo = await page3.evaluate(() => {
      return {
        title: document.title,
        h1Count: document.querySelectorAll('h1').length,
        pCount: document.querySelectorAll('p').length,
        linkCount: document.querySelectorAll('a').length,
        bodyText: document.body.textContent?.substring(0, 200),
      };
    });
    console.log(`✅ Titolo: "${pageInfo.title}"`);
    console.log(`✅ H1: ${pageInfo.h1Count}, Paragrafi: ${pageInfo.pCount}, Link: ${pageInfo.linkCount}`);
    console.log(`✅ Testo: "${pageInfo.bodyText?.trim().substring(0, 100)}..."`);

    await context3.close();
    console.log('');

    // Test 4: Screenshot (se supportato)
    console.log('═'.repeat(50));
    console.log('📄 TEST 4: Screenshot');
    console.log('═'.repeat(50));
    const context4 = await browser.createBrowserContext();
    const page4 = await context4.newPage();

    await page4.goto('https://example.com', {
      waitUntil: 'load',
      timeout: 30000,
    });

    try {
      const screenshotPath = '/tmp/lightpanda_test_screenshot.png';
      await page4.screenshot({ path: screenshotPath });
      console.log(`✅ Screenshot salvato: ${screenshotPath}`);
    } catch (err) {
      console.log(`⚠️  Screenshot non supportato: ${err.message}`);
    }

    await context4.close();

  } catch (err) {
    console.error('❌ Errore durante il test:', err.message);
    console.error(err.stack);
  } finally {
    try {
      await browser.disconnect();
    } catch (e) {}
    console.log('');
    console.log('═'.repeat(50));
    console.log('👋 Test completato!');
    console.log('═'.repeat(50));
  }
}

testLightpanda();
