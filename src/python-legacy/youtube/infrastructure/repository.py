import logging
from typing import Optional, List, Dict, Any, Union
from threading import Lock
import os
import time

# Adjust import based on where this file ends up relative to project root
# Using direct import since we are in a module structure now
from routes.youtube.utils import _get_video_upload_storage, _find_group_obj
from routes.youtube.channels import get_channels_data

logger = logging.getLogger(__name__)

class UploadsRepository:
    """
    Infrastructure layer for accessing VideoUploadStorage and Channels data.
    """
    
    def __init__(self):
        self._storage = None

    @property
    def storage(self):
        if not self._storage:
            self._storage = _get_video_upload_storage()
        return self._storage

    def get_upload_state(self, output_video_id: str):
        return self.storage.get_upload_state(output_video_id)

    def list_uploads(self, status: Optional[str], limit: int, channel_ids: Optional[List[str]], only_missing_thumbnail: bool, exclude_deleted: bool = False):
        return self.storage.list_uploads(
            status=(str(status).strip().upper() if status else None),
            limit=limit,
            channel_ids=channel_ids,
            only_missing_thumbnail=bool(only_missing_thumbnail),
            exclude_statuses=(["DELETED"] if exclude_deleted and not status else None),
        )

    def mark_thumbnail_set(self, output_video_id: str, thumbnail_url: str, thumbnail_set: bool):
        self.storage.mark_thumbnail_set(
            output_video_id=output_video_id, 
            thumbnail_url=thumbnail_url, 
            thumbnail_set=thumbnail_set
        )

    def get_job_spec(self, job_id: str):
        return self.storage.get_job_spec(job_id)

    def get_channels_and_groups(self, validate_tokens: bool = False, force_refresh: bool = False):
        return get_channels_data(validate_tokens=validate_tokens, force_refresh=force_refresh)

    def find_group_channels(self, group_name: str) -> Optional[List[str]]:
        yt = self.get_channels_and_groups(validate_tokens=False)
        groups = yt.get("groups") or []
        group_obj = _find_group_obj(groups, group_name)
        if group_obj:
            return [str(x) for x in (group_obj.get("channels") or []) if x]
        return None
