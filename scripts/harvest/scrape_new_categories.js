#!/usr/bin/env node
/**
 * Script per aggiungere nuove categorie al DB Artlist
 * Usage: node scripts/scrape_new_categories.js
 */

import { getDB, categories, searchTerms, videoLinks, closeDB } from '../../src/node-scraper/src/db.js';
import puppeteer from 'puppeteer-core';

const LIGHTPANDA_WS = process.env.BROWSER_WS || process.env.LIGHTPANDA_WS || 'ws://127.0.0.1:9222';
const ARTLIST_BASE = 'https://artlist.io/stock-footage/search?terms=';
const MAX_CATEGORIES = Number.parseInt(process.env.MAX_CATEGORIES || '0', 10);
const MAX_TERMS_PER_CATEGORY = Number.parseInt(process.env.MAX_TERMS_PER_CATEGORY || '0', 10);

// NUOVE CATEGORIE da aggiungere
const NEW_CATEGORIES = [
  {
    name: 'Sports',
    terms: ['sports', 'football', 'basketball', 'tennis', 'running', 'fitness', 'gym', 'workout']
  },
  {
    name: 'Food',
    terms: ['food', 'cooking', 'chef', 'restaurant', 'meal', 'dining', 'kitchen', 'recipe']
  },
  {
    name: 'Music',
    terms: ['music', 'concert', 'festival', 'drums', 'guitar', 'piano', 'dj', 'dancing']
  },
  {
    name: 'Travel',
    terms: ['travel', 'airplane', 'airport', 'train', 'hotel', 'vacation', 'tourism', 'passport']
  },
  {
    name: 'Business',
    terms: ['business', 'meeting', 'office', 'presentation', 'handshake', 'contract', 'success', 'startup']
  },
  {
    name: 'Abstract',
    terms: ['abstract', 'background', 'texture', 'colorful', 'geometric', 'pattern', 'gradient', 'minimal']
  }
];

async function scrapeCategory(categoryName, searchTerm, page) {
  console.log(`\n📹 Scraping: "${categoryName}" → "${searchTerm}"`);

  const videoUrls = new Set();
  const videoPattern = /footage-hls.*\.m3u8/i;

  const url = `${ARTLIST_BASE}${encodeURIComponent(searchTerm)}`;
  console.log(`   URL: ${url}`);

  try {
    await page.goto(url, {
      waitUntil: "networkidle0",
      timeout: 90000
    });

    const title = await page.title();
    if (title.includes('Just a moment')) {
      console.log('   ⚠️ Cloudflare detected, waiting 30s...');
      await new Promise(r => setTimeout(r, 30000));
    }

    // Scroll to load more
    await page.evaluate(async () => {
      for (let i = 0; i < 15; i++) {
        window.scrollBy(0, 800);
        await new Promise(r => setTimeout(r, 1500));
      }
    });

    await new Promise(r => setTimeout(r, 10000));

    // Extract m3u8 URLs from network or page
    const urls = await page.evaluate(() => {
      const found = [];
      // Look in video elements
      document.querySelectorAll('video').forEach(v => {
        if (v.src && v.src.includes('playlist')) found.push(v.src);
      });
      // Look in script tags
      document.querySelectorAll('script').forEach(s => {
        if (s.textContent) {
          const matches = s.textContent.match(/https:\/\/[^"\']+_playlist[^"\']*\.m3u8/g);
          if (matches) found.push(...matches);
        }
      });
      return found;
    });

    urls.forEach(u => videoUrls.add(u));

    console.log(`   ✅ Found ${videoUrls.size} unique URLs`);
    return Array.from(videoUrls);

  } catch (error) {
    console.error(`   ❌ Error: ${error.message}`);
    return [];
  }
}

async function main() {
  console.log('=' .repeat(70));
  console.log('🎬 Adding New Categories to Artlist DB');
  console.log('=' .repeat(70));

  const db = getDB();

  // Connect to browser
  console.log('\n🔗 Connecting to browser...');
  const browser = await puppeteer.connect({
    browserWSEndpoint: LIGHTPANDA_WS,
  });
  const context = await browser.createBrowserContext();
  const page = await context.newPage();

  let totalTerms = 0;
  let totalVideos = 0;

  const categoriesToRun = MAX_CATEGORIES > 0 ? NEW_CATEGORIES.slice(0, MAX_CATEGORIES) : NEW_CATEGORIES;

  for (const cat of categoriesToRun) {
    console.log(`\n{'='.repeat(70)}`);
    console.log(`📁 Category: ${cat.name}`);
    console.log(`{'='.repeat(70)}`);

    // Create category in DB
    categories.add(cat.name, `Artlist ${cat.name} footage`);
    const category = categories.getByName(cat.name);

    if (!category) {
      console.error(`   ❌ Failed to create category: ${cat.name}`);
      continue;
    }

    console.log(`   ✅ Category ID: ${category.id}`);

    // Add search terms
    searchTerms.addMultiple(cat.name, cat.terms);

    const termsToRun = MAX_TERMS_PER_CATEGORY > 0 ? cat.terms.slice(0, MAX_TERMS_PER_CATEGORY) : cat.terms;

    // Scrape each term
    for (const term of termsToRun) {
      const urls = await scrapeCategory(cat.name, term, page);

      if (urls.length > 0) {
        // Add video links to DB
        const metadata = urls.map(u => ({
          video_id: u.match(/\/([^\/]+)_playlist/)?.[1] || 'unknown',
          width: 1920,
          height: 1080,
          duration: 10000, // 10s default
          size: 0
        }));

        const count = videoLinks.addMultipleWithSource(
          cat.name, term, urls, 'artlist', metadata
        );

        console.log(`   ✅ Added ${count} videos to DB`);
        totalVideos += count;
      }

      // Mark term as scraped
      searchTerms.markScraped(cat.name, term, urls.length);
      totalTerms++;

      // Small delay between terms
      await new Promise(r => setTimeout(r, 3000));
    }
  }

  // Cleanup
  await context.close();
  await browser.disconnect();
  closeDB();

  console.log(`\n{'='.repeat(70)}`);
  console.log('✅ COMPLETE');
  console.log(`{'='.repeat(70)}`);
    console.log(`   Categories: ${categoriesToRun.length}`);
  console.log(`   Terms: ${totalTerms}`);
  console.log(`   Videos: ${totalVideos}`);

  process.exit(0);
}

main().catch(err => {
  console.error('Fatal error:', err);
  process.exit(1);
});
