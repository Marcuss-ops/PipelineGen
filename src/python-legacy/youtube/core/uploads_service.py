"""
Core domain logic for YouTube uploads management.
"""
from typing import Dict, Any, List, Optional
import time
import os
from threading import Lock
from . import uploads_service # self-reference issue? No, this is the file.

# Imports for dependencies (Interfaces)
# In clean architecture, we should define interfaces here, but for pragmatic refactoring
# we will accept that the service uses the infrastructure classes passed to it.

class UploadsService:
    def __init__(self, repository, api_adapter):
        self.repo = repository
        self.api = api_adapter
        
        # Cache setup
        self._cache_lock = Lock()
        self._cache: Dict[str, Any] = {}
        self._cache_ts: Dict[str, float] = {}
        self._cache_ttl = int(os.environ.get("YOUTUBE_UPLOADS_LIST_CACHE_TTL_SEC", "5"))
        
        self._privacy_cache_lock = Lock()
        self._privacy_cache: Dict[str, Any] = {"updated_at": 0.0, "data": {}}

    def _get_cache_key(self, *, group: Optional[str], status: Optional[str], only_missing_thumbnail: bool, limit: int, channel_ids: Optional[List[str]]) -> str:
        cids = ",".join(sorted(channel_ids)) if channel_ids else "all"
        return f"g={group}:s={status}:omt={only_missing_thumbnail}:l={limit}:c={cids}"

    def clear_cache(self):
        with self._cache_lock:
            self._cache.clear()
            self._cache_ts.clear()

    def get_upload_privacy_map(self) -> Dict[str, str]:
        with self._privacy_cache_lock:
            data = self._privacy_cache.get("data", {})
            return data if isinstance(data, dict) else {}

    def update_upload_privacy_map(self, updates: Dict[str, str]):
        with self._privacy_cache_lock:
            data = self._privacy_cache.get("data", {})
            if not isinstance(data, dict):
                data = {}
            data.update(updates)
            self._privacy_cache["data"] = data
            self._privacy_cache["updated_at"] = time.time()

    def _enrich_upload_row(self, row: Dict[str, Any], channel_titles: Dict[str, str]) -> Dict[str, Any]:
        """Transform DB row into API response format."""
        r2 = dict(row or {})
        
        try:
            from ...utils import _youtube_video_url, _youtube_channel_url
        except ImportError:
            # Fallback inline or mock
            _youtube_video_url = lambda x: f"https://youtu.be/{x}"
            _youtube_channel_url = lambda x: f"https://youtube.com/channel/{x}"

        output_video_id = str(r2.get("output_video_id") or "").strip()
        job_id = str(r2.get("job_id") or "").strip() or None
        channel_id = str(r2.get("channel_id") or "").strip() or None
        youtube_title = r2.get("youtube_title")
        youtube_video_id = r2.get("youtube_video_id")

        youtube_group = None
        video_name = None
        project_name = None
        try:
            if job_id:
                js = self.repo.get_job_spec(job_id)
                if js and isinstance(js.spec_json, dict):
                    youtube_group = js.spec_json.get("youtube_group")
                    video_name = js.spec_json.get("video_name")
                    project_name = js.spec_json.get("project_name")
        except Exception:
            pass

        title_final = (
            (str(youtube_title).strip() if isinstance(youtube_title, str) else "")
            or (str(video_name).strip() if isinstance(video_name, str) else "")
            or "Untitled"
        )
        channel_title = channel_titles.get(channel_id or "", channel_id or "N/A")

        vid = str(youtube_video_id).strip() if isinstance(youtube_video_id, str) else ""
        r2["youtube_url"] = _youtube_video_url(vid) if vid else None
        r2["channel_url"] = _youtube_channel_url(channel_id) if channel_id else None
        r2["channel_title"] = channel_title
        r2["youtube_group"] = youtube_group
        r2["project_name"] = project_name
        r2["video_name"] = video_name
        r2["title"] = title_final

        privacy_status = "unknown"
        if vid:
            try:
                privacy_status = self.get_upload_privacy_map().get(vid) or "unknown"
            except Exception:
                privacy_status = "unknown"
        r2["privacy_status"] = privacy_status
        r2["privacy"] = privacy_status if privacy_status != "unknown" else None
        r2["privacyStatus"] = privacy_status

        # Compat fields
        r2["id"] = vid or (output_video_id or None)
        r2["videoId"] = vid or None
        r2["youtubeVideoId"] = vid or None
        r2["youtubeUrl"] = r2.get("youtube_url")
        r2["outputVideoId"] = output_video_id or None
        r2["jobId"] = job_id
        r2["channelId"] = channel_id
        r2["channelTitle"] = channel_title
        r2["group"] = youtube_group
        r2["project"] = project_name
        return r2

    def list_uploads(self, group: Optional[str], status: Optional[str], only_missing_thumbnail: bool, limit: int, reset_cache: bool) -> Dict[str, Any]:
        if reset_cache:
            self.clear_cache()

        channel_ids = None
        group_name = (group or "").strip()
        if group_name:
            channel_ids = self.repo.find_group_channels(group_name)
            # If group exists but no channels, channel_ids is [], which is valid.
            # If group not found, repo returns None?
            if channel_ids is None:
                # Need to handle "Group not found" error, maybe return None or raise
                raise ValueError(f"Gruppo non trovato: {group_name}")

        cache_key = self._get_cache_key(
            group=group_name or None,
            status=status,
            only_missing_thumbnail=bool(only_missing_thumbnail),
            limit=int(limit),
            channel_ids=channel_ids,
        )

        if self._cache_ttl > 0:
            with self._cache_lock:
                ts = self._cache_ts.get(cache_key)
                cached = self._cache.get(cache_key)
                if cached is not None and ts is not None and (time.time() - float(ts)) < float(self._cache_ttl):
                    return cached

        rows = self.repo.list_uploads(
            status=status,
            limit=limit,
            channel_ids=channel_ids,
            only_missing_thumbnail=only_missing_thumbnail,
            exclude_deleted=True # Defaulting to True as per original logic if status is None?
        )

        # Build map of channel titles efficiently?
        # Original code called _get_channel_title_map_cached()
        # We can implement that in repo or here
        channel_titles = {}
        group_by_channel: Dict[str, str] = {}
        try:
            ch_data = self.repo.get_channels_and_groups(validate_tokens=False)
            channels = ch_data.get("channels") or []
            groups = ch_data.get("groups") or []

            # Build channelId -> groupName from groups.json (if available)
            if isinstance(groups, list):
                for g in groups:
                    if not isinstance(g, dict):
                        continue
                    gname = str(g.get("name") or "").strip()
                    if not gname:
                        continue
                    for cid in (g.get("channels") or []):
                        c = str(cid or "").strip()
                        if c:
                            group_by_channel[c] = gname

            # Accept both legacy and refactored shapes (channelId/id).
            channel_titles = {
                str((c.get("id") or c.get("channelId") or c.get("channel_id") or "")).strip():
                    (c.get("title") or c.get("channelTitle") or c.get("name") or c.get("id") or c.get("channelId"))
                for c in channels
                if (c.get("id") or c.get("channelId") or c.get("channel_id"))
            }
        except:
            pass

        out = []
        for r in rows:
            if not isinstance(r, dict):
                continue
            row2 = self._enrich_upload_row(r, channel_titles)
            try:
                if not row2.get("youtube_group"):
                    cid = str(row2.get("channel_id") or row2.get("channelId") or "").strip()
                    if cid and cid in group_by_channel:
                        row2["youtube_group"] = group_by_channel.get(cid)
                        row2["group"] = row2["youtube_group"]
            except Exception:
                pass
            out.append(row2)

        payload = {"ok": True, "group": group_name or None, "items": out, "count": len(out)}
        
        if self._cache_ttl > 0:
            with self._cache_lock:
                self._cache[cache_key] = payload
                self._cache_ts[cache_key] = time.time()
                
        return payload

    def apply_thumbnail(self, channel_id: str, youtube_video_id: str, thumbnail_url: str):
        return self.api.apply_thumbnail(channel_id, youtube_video_id, thumbnail_url)
