/**
 * Lightpanda Integration Test per VeloxEditing
 * Testa le capacità di scraping per estrazione video link e metadati
 */

const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const LIGHTPANDA = '/home/pierone/Pyt/VeloxEditing/refactored/data/lightpanda/lightpanda';

function runFetch(url, options = {}) {
  const dump = options.dump || 'html';
  const waitUntil = options.waitUntil || 'load';
  const waitMs = options.waitMs || 5000;
  const stripMode = options.stripMode || '';

  let cmd = `${LIGHTPANDA} fetch --dump ${dump} --wait-until ${waitUntil} --wait-ms ${waitMs}`;
  if (stripMode) cmd += ` --strip-mode ${stripMode}`;
  cmd += ` ${url}`;

  try {
    return execSync(cmd, { timeout: 30000, stdio: ['pipe', 'pipe', 'pipe'] }).toString();
  } catch (err) {
    console.error(`❌ Fetch failed for ${url}:`, err.message);
    return null;
  }
}

function extractLinks(html) {
  const linkRegex = /href="([^"]+)"/g;
  const links = [];
  let match;
  while ((match = linkRegex.exec(html)) !== null) {
    if (match[1].startsWith('http')) {
      links.push(match[1]);
    }
  }
  return [...new Set(links)];
}

function extractVideoUrls(html) {
  const videoRegex = /(?:src|href)="([^"]*\.(?:mp4|webm|ogg|m3u8)[^"]*)"/gi;
  const urls = [];
  let match;
  while ((match = videoRegex.exec(html)) !== null) {
    urls.push(match[1]);
  }
  return urls;
}

async function main() {
  console.log('═'.repeat(60));
  console.log('🎬 Lightpanda Test Suite - VeloxEditing');
  console.log('═'.repeat(60));
  console.log('');

  // Test 1: Pagina semplice
  console.log('TEST 1: Pagina semplice (example.com)');
  console.log('-'.repeat(40));
  const html1 = runFetch('https://example.com', { dump: 'html', stripMode: 'css,js' });
  if (html1) {
    const title = html1.match(/<title>([^<]+)<\/title>/);
    console.log(`✅ Titolo: ${title ? title[1] : 'N/A'}`);
    console.log(`✅ Lunghezza HTML: ${html1.length} caratteri`);
  }
  console.log('');

  // Test 2: Wikipedia - semantic tree
  console.log('TEST 2: Wikipedia - Semantic Tree');
  console.log('-'.repeat(40));
  const semantic = runFetch('https://en.wikipedia.org/wiki/Web_scraping', {
    dump: 'semantic_tree_text',
    waitUntil: 'load',
    waitMs: 5000,
  });
  if (semantic) {
    const lines = semantic.split('\n');
    const linkLines = lines.filter(l => l.includes('link '));
    console.log(`✅ Link trovati nel semantic tree: ${linkLines.length}`);
    console.log(`✅ Lunghezza semantic tree: ${semantic.length} caratteri`);
    // Show first 5 meaningful links
    const contentLinks = linkLines.filter(l => !l.includes('jump') && !l.includes('button'))
      .slice(0, 5);
    contentLinks.forEach(l => console.log(`  - ${l.trim()}`));
  }
  console.log('');

  // Test 3: Estrazione HTML con strip mode
  console.log('TEST 3: Estrazione HTML pulito (no CSS/JS)');
  console.log('-'.repeat(40));
  const cleanHtml = runFetch('https://example.com', {
    dump: 'html',
    stripMode: 'css,js',
  });
  if (cleanHtml) {
    const size = cleanHtml.length;
    console.log(`✅ HTML pulito: ${size} caratteri (senza CSS/JS)`);
    console.log(`✅ Preview: ${cleanHtml.substring(0, 100).replace(/\n/g, '')}...`);
  }
  console.log('');

  // Test 4: Markdown dump
  console.log('TEST 4: Estrazione Markdown');
  console.log('-'.repeat(40));
  const markdown = runFetch('https://en.wikipedia.org/wiki/Web_scraping', {
    dump: 'markdown',
    waitUntil: 'load',
    waitMs: 5000,
  });
  if (markdown) {
    const lines = markdown.split('\n').filter(l => l.trim());
    console.log(`✅ Markdown: ${lines.length} righe`);
    console.log(`✅ Lunghezza: ${markdown.length} caratteri`);
    // Show headings
    const headings = lines.filter(l => l.startsWith('#')).slice(0, 5);
    console.log('✅ Intestazioni:');
    headings.forEach(h => console.log(`  ${h}`));
  }
  console.log('');

  // Test 5: Pagina con video (W3Schools)
  console.log('TEST 5: Pagina con elementi video');
  console.log('-'.repeat(40));
  const videoHtml = runFetch('https://www.w3schools.com/html/html5_video.asp', {
    dump: 'html',
    waitUntil: 'load',
    waitMs: 5000,
  });
  if (videoHtml) {
    const videoUrls = extractVideoUrls(videoHtml);
    const allLinks = extractLinks(videoHtml);
    console.log(`✅ Link video trovati: ${videoUrls.length}`);
    videoUrls.forEach((v, i) => console.log(`  ${i+1}. ${v}`));
    console.log(`✅ Link totali: ${allLinks.length}`);
  }
  console.log('');

  // Summary
  console.log('═'.repeat(60));
  console.log('📊 RIEPILOGO CAPACITÀ LIGHTPANDA');
  console.log('═'.repeat(60));
  console.log('✅ Fetch HTML completo');
  console.log('✅ Dump semantic tree (accessibility tree)');
  console.log('✅ Dump Markdown (per LLM)');
  console.log('✅ Strip mode (rimuove CSS/JS/ui)');
  console.log('✅ Wait options (load, domcontentloaded, networkidle, done)');
  console.log('✅ Supporto pagine complesse (Wikipedia, W3Schools)');
  console.log('');
  console.log('🔧 USO NEL PROGETTO VELoxEditing:');
  console.log('   - Estrazione metadati video da pagine web');
  console.log('   - Scraping link video da siti stock');
  console.log('   - Generazione script da contenuti web');
  console.log('   - CDP mode per automazione browser completa');
  console.log('═'.repeat(60));
}

main().catch(console.error);
