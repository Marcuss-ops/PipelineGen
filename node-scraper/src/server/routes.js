// Public route facade. Endpoint implementations live in one module per concern;
// existing imports from ./routes.js and all URL paths remain unchanged.
import { handleDetail } from './detail-route.js';
import { handleDiscoverApi } from './discover-route.js';
import { handleSearch } from './search-route.js';
import { handleV1ClipSearch } from './v1-search-route.js';
import { handleCatalogSync, handleCatalogSyncSchedule, handleCatalogSyncStatus } from './catalog-sync-route.js';
import { handleDownload } from './download-route.js';
import { handleHealth } from './health-route.js';
import fs from 'fs';
import { execSync } from 'child_process';

export { handleDetail, handleDiscoverApi, handleSearch, handleV1ClipSearch, handleCatalogSync, handleCatalogSyncSchedule, handleCatalogSyncStatus, handleDownload, handleHealth };

export async function dispatchRequest(req, res, ctx) {
  const url = new URL(req.url, `http://localhost:${ctx.config.PORT}`);
  if (url.pathname === '/search') {
    await handleSearch(req, res, ctx);
  } else if (url.pathname === '/v1/clips/search') {
    await handleV1ClipSearch(req, res, ctx);
  } else if (url.pathname === '/v1/catalog/sync') {
    await handleCatalogSync(req, res, ctx);
  } else if (url.pathname === '/v1/catalog/schedule') {
    handleCatalogSyncSchedule(req, res, ctx);
  } else if (url.pathname.startsWith('/v1/catalog/sync/')) {
    const syncId = decodeURIComponent(url.pathname.slice('/v1/catalog/sync/'.length));
    handleCatalogSyncStatus(req, res, ctx, syncId);
  } else if (url.pathname === '/detail') {
    await handleDetail(req, res, ctx);
  } else if (url.pathname === '/download') {
    await handleDownload(req, res, ctx);
  } else if (url.pathname === '/discover-api') {
    await handleDiscoverApi(req, res, ctx);
  } else if (url.pathname === '/health') {
    handleHealth(req, res, ctx);
  } else if (url.pathname === '/v1/health') {
    handleHealth(req, res, ctx);
  } else if (url.pathname === '/mock-video.mp4') {
    const mockFile = '/tmp/mock-video-serv.mp4';
    if (!fs.existsSync(mockFile)) {
      try {
        execSync(`ffmpeg -y -f lavfi -i color=c=blue:s=1920x1080:d=1 -f lavfi -i anullsrc=cl=mono:r=16000 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "${mockFile}"`);
      } catch (err) {
        console.error('Failed to pre-generate mock video:', err);
      }
    }
    res.writeHead(200, { 'Content-Type': 'video/mp4' });
    fs.createReadStream(mockFile).pipe(res);
  } else {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: `Unknown path: ${url.pathname}` }));
  }
}
