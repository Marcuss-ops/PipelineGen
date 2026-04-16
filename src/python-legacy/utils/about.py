__version__ = "2.0.0"
__author__ = "VideoCollegaTanti Team"
__description__ = "AI-powered video generation application"


def get_version() -> str:
    """Get the current version of the application."""
    return __version__


def get_info() -> dict:
    """Get application information."""
    return {
        "name": "VideoCollegaTanti",
        "version": __version__,
        "author": __author__,
        "description": __description__,
        "modules": [
            "config",
            "utils",
            "audio_processing",
            "video_processing",
            "web_automation",
            "workflow",
            "gradio_ui"
        ]
    }