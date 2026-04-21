import puppeteer from 'puppeteer-core';

const LIGHTPANDA_WS = 'ws://127.0.0.1:9222';

async function testLightpanda() {
  console.log('🚀 Test Lightpanda con pagine semplici...\n');
  
  const browser = await puppeteer.connect({
    browserWSEndpoint: LIGHTPANDA_WS,
  });

  const context = await browser.createBrowserContext();
  const page = await context.newPage();

  try {
    // Test 1: Pagina semplice
    console.log('📄 Test 1: Wikipedia (HTML semplice)');
    await page.goto('https://en.wikipedia.org/wiki/Web_scraping', { 
      waitUntil: "networkidle0",
      timeout: 30000 
    });
    
    const title = await page.title();
    console.log(`✅ Titolo: ${title}\n`);

    // Test 2: Pagina con video pubblici
    console.log('📄 Test 2: Pagina con video pubblici');
    await page.goto('https://www.w3schools.com/html/html5_video.asp', { 
      waitUntil: "networkidle0",
      timeout: 30000 
    });

    const videos = await page.evaluate(() => {
      const videoElements = document.querySelectorAll('video, source');
      return Array.from(videoElements).map(v => ({
        tag: v.tagName,
        src: v.getAttribute('src')
      }));
    });

    console.log(`✅ Trovati ${videos.length} elementi video:`);
    videos.forEach(v => console.log(`  - ${v.tag}: ${v.src}`));

    // Test 3: Estrazione link da pagina
    console.log('\n📄 Test 3: Estrazione tutti i link da pagina corrente');
    const links = await page.evaluate(() => {
      return Array.from(document.querySelectorAll('a[href]'))
        .map(a => a.href)
        .slice(0, 10);
    });
    
    console.log(`✅ Primi ${links.length} link:`);
    links.forEach((link, i) => console.log(`  ${i+1}. ${link}`));

  } catch (error) {
    console.error('❌ Errore:', error.message);
  } finally {
    try {
      await page.close();
      await context.close();
      await browser.disconnect();
    } catch (e) {}
    console.log('\n👋 Test completato!');
  }
}

testLightpanda();
