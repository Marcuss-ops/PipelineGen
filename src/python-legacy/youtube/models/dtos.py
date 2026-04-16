from typing import List, Optional, Dict, Any
from pydantic import BaseModel

class ApplyThumbnailRequest(BaseModel):
    thumbnail_url: str

class UpdateVideoTitleRequest(BaseModel):
    channel_id: str
    title: str

class PublishVideoRequest(BaseModel):
    channel_id: str
    privacy: str = "public"
    publish_time: Optional[str] = None
    title: Optional[str] = None

class PublishUploadByOutputIdRequest(BaseModel):
    privacy: str = "public"
    publish_time: Optional[str] = None
    title: Optional[str] = None

class ForgetUploadRequest(BaseModel):
    hard: bool = False
    clear_file_hash: bool = True
    reason: Optional[str] = None

class BulkApplyThumbnailsRequest(BaseModel):
    thumbnail_url: Optional[str] = None
    output_video_ids: Optional[List[str]] = None
    mapping: Optional[Dict[str, str]] = None
    stop_on_quota: bool = True

class AutoMatchThumbnailsRequest(BaseModel):
    group: Optional[str] = None
    job_id: Optional[str] = None
    output_video_ids: Optional[List[str]] = None
    status: Optional[str] = "UPLOADED"
    only_missing_thumbnail: bool = True
    limit: int = 2000
    thumbnails_by_lang: Optional[Dict[str, str]] = None
    thumbnails: Optional[List[Dict[str, str]]] = None # [{"filename":..., "url":...}]
    default_thumbnail_url: Optional[str] = None
    apply: bool = True
    stop_on_quota: bool = True
    
class UploadStatesByOutputIdsRequest(BaseModel):
    output_video_ids: List[str]

class ApplyThumbnailByYoutubeVideoIdRequest(BaseModel):
    channel_id: str
    thumbnail_url: Optional[str] = None
    filename: Optional[str] = None

class ForgetUploadsBulkRequest(BaseModel):
    output_video_ids: Optional[List[str]] = None
    group: Optional[str] = None
    channel_id: Optional[str] = None
    status: Optional[str] = None
    only_missing_thumbnail: bool = False
    limit: int = 1000
    hard: bool = False
    clear_file_hash: bool = True

class ReconcileUploadsRequest(BaseModel):
    check_all: bool = False
    groups: Optional[List[str]] = None

class DriveImportRequest(BaseModel):
    drive_link: str
    group_name: str
    project_name: Optional[str] = None
    auto_assign: bool = True
