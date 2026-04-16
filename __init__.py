"""VideoCollegaTanti - Refactored Video Generation Application.

This package provides a modular, maintainable architecture for automated video generation
combining AI transcription, content analysis, and video processing.

Modules:
    config: Configuration management and constants
    utils: Common utility functions
    audio_processing: Audio transcription and processing
    video_processing: Video creation and manipulation
    web_automation: Browser automation for AI services
    workflow: Main workflow orchestration
    gradio_ui: User interface components

Example:
    >>> from refactored import launch_app
    >>> launch_app()
"""

# Import metadata from about.py
# from .about import __version__, __author__, __description__, get_version, get_info

# Import main components for easy access
# from .config import ConfigWrapper
# from .workflow import VideoGenerationWorkflow, create_workflow
# from .gradio_ui import launch_app
# from .multi_video_interface import launch_multi_video_app, create_multi_video_interface
# from .audio_processing import AudioProcessor
# from .video_processing import crea_effetto_typewriting_nomi
# from .web_automation import WebDriverManager, attendi_textarea_pronta, chiudi_popup, assicura_focus_textarea, estrai_risposta, invia_prompt_clipboard
# from .audio_processing import trascrivi_audio_parallel, detect_audio_language
# from .workflow import estrai_annotazioni_da_qwen, _execute_phase_a_data_acquisition

# Define public API
__all__ = [
    # From about
    "__version__",
    "__author__",
    "__description__",
    "get_version",
    "get_info",
    
    # Configuration
    "ConfigWrapper",
    
    # Workflow
    "VideoGenerationWorkflow",
    "create_workflow",
    "estrai_annotazioni_da_qwen_sync",
    
    # UI
    "launch_app",
    "launch_multi_video_app",
    "create_multi_video_interface",
    
    # Processing
    "AudioProcessor",
    "crea_effetto_typewriting_nomi",
    
    # Automation
    "WebDriverManager",
]