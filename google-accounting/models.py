from typing import Literal, Optional
from pydantic import BaseModel

class DownloadRequest(BaseModel):
    video_id: str
    file_type: Literal["video", "image", "all"] = "all"
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None


class GenerateRequest(BaseModel):
    video_id: str
    prompt: str
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class CharacterVideoRequest(BaseModel):
    video_id: str = "new"
    character_id: str
    prompt: Optional[str] = None
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class AvatarRequest(BaseModel):
    video_id: str = "new"
    script: str
    avatar_id: Optional[str] = None  # e.g., "professional_male", "friendly_female"
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class VidsImageRequest(BaseModel):
    video_id: str = "new"
    prompt: str
    num_images: Optional[int] = 1
    style: Optional[str] = None
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class SyncRequest(BaseModel):
    video_id: str
    file_type: Literal["video", "image", "all"] = "all"
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
