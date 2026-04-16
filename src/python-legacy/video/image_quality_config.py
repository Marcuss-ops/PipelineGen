"""
Image Quality Enhancement Configuration
Allows users to customize image quality settings for different computer capabilities.
"""

import json
import os
from typing import Dict, Any

class ImageQualityConfig:
    """Configuration manager for image quality enhancement settings."""
    
    def __init__(self, config_file: str = None):
        if config_file is None:
            config_file = os.path.join(os.path.dirname(__file__), 'image_quality_settings.json')
        
        self.config_file = config_file
        self.settings = self._load_default_settings()
        self._load_user_settings()
    
    def _load_default_settings(self) -> Dict[str, Any]:
        """Load default image quality settings."""
        return {
            "enhancement": {
                "enabled": True,
                "auto_enhance": True,
                "default_level": "auto",  # light, medium, aggressive, auto
                "max_processing_time": 30,  # seconds per image
                "skip_large_images": True,  # Skip images larger than max_size_mb
                "max_size_mb": 50
            },
            "quality_thresholds": {
                "min_resolution": [400, 400],  # [width, height]
                "target_resolution": [1920, 1080],
                "min_sharpness": 50.0,
                "min_contrast": 30.0,
                "min_brightness_variance": 20.0,
                "min_resolution_score": 0.3
            },
            "enhancement_levels": {
                "light": {
                    "upscale_factor": 1.5,
                    "sharpness_factor": 1.1,
                    "contrast_factor": 1.05,
                    "brightness_factor": 1.02,
                    "color_factor": 1.05
                },
                "medium": {
                    "upscale_factor": 2.0,
                    "sharpness_factor": 1.2,
                    "contrast_factor": 1.1,
                    "brightness_factor": 1.05,
                    "color_factor": 1.1
                },
                "aggressive": {
                    "upscale_factor": 2.5,
                    "sharpness_factor": 1.3,
                    "contrast_factor": 1.15,
                    "brightness_factor": 1.08,
                    "color_factor": 1.15,
                    "noise_reduction": True
                }
            },
            "performance": {
                "cpu_threads": "auto",  # auto, or specific number
                "memory_limit_mb": 1024,  # Maximum memory usage
                "batch_size": 5,  # Number of images to process simultaneously
                "cache_enhanced_images": True
            },
            "compatibility": {
                "low_end_mode": False,  # Reduces quality for better performance
                "skip_heavy_operations": False,  # Skip CPU-intensive enhancements
                "fallback_to_basic": True  # Use basic enhancement if advanced fails
            }
        }
    
    def _load_user_settings(self):
        """Load user-customized settings from file."""
        if os.path.exists(self.config_file):
            try:
                with open(self.config_file, 'r', encoding='utf-8') as f:
                    user_settings = json.load(f)
                    self._merge_settings(user_settings)
            except Exception as e:
                print(f"Warning: Could not load user settings from {self.config_file}: {e}")
    
    def _merge_settings(self, user_settings: Dict[str, Any]):
        """Merge user settings with default settings."""
        def merge_dict(default: dict, user: dict):
            for key, value in user.items():
                if key in default:
                    if isinstance(default[key], dict) and isinstance(value, dict):
                        merge_dict(default[key], value)
                    else:
                        default[key] = value
                else:
                    default[key] = value
        
        merge_dict(self.settings, user_settings)
    
    def save_settings(self):
        """Save current settings to file."""
        try:
            os.makedirs(os.path.dirname(self.config_file), exist_ok=True)
            with open(self.config_file, 'w', encoding='utf-8') as f:
                json.dump(self.settings, f, indent=2, ensure_ascii=False)
        except Exception as e:
            print(f"Warning: Could not save settings to {self.config_file}: {e}")
    
    def get(self, key_path: str, default=None):
        """Get a setting value using dot notation (e.g., 'enhancement.enabled')."""
        keys = key_path.split('.')
        value = self.settings
        
        for key in keys:
            if isinstance(value, dict) and key in value:
                value = value[key]
            else:
                return default
        
        return value
    
    def set(self, key_path: str, value):
        """Set a setting value using dot notation."""
        keys = key_path.split('.')
        setting = self.settings
        
        for key in keys[:-1]:
            if key not in setting:
                setting[key] = {}
            setting = setting[key]
        
        setting[keys[-1]] = value
    
    def is_enhancement_enabled(self) -> bool:
        """Check if image enhancement is enabled."""
        return self.get('enhancement.enabled', True)
    
    def should_auto_enhance(self) -> bool:
        """Check if auto enhancement is enabled."""
        return self.get('enhancement.auto_enhance', True)
    
    def get_enhancement_level(self) -> str:
        """Get the default enhancement level."""
        return self.get('enhancement.default_level', 'auto')
    
    def is_low_end_mode(self) -> bool:
        """Check if low-end mode is enabled."""
        return self.get('compatibility.low_end_mode', False)
    
    def get_quality_thresholds(self) -> Dict[str, Any]:
        """Get quality threshold settings."""
        return self.get('quality_thresholds', {})
    
    def get_performance_settings(self) -> Dict[str, Any]:
        """Get performance-related settings."""
        return self.get('performance', {})
    
    def create_user_config_template(self, output_path: str = None):
        """Create a user-friendly configuration template file."""
        if output_path is None:
            output_path = os.path.join(os.path.dirname(self.config_file), 'image_quality_settings_template.json')
        
        template = {
            "_comment": "Image Quality Enhancement Settings - Customize these values based on your computer's capabilities",
            "_instructions": {
                "enhancement_levels": "light = fast, medium = balanced, aggressive = best quality but slower",
                "low_end_mode": "Set to true for older/slower computers",
                "auto_enhance": "Automatically enhance low-quality images",
                "performance": "Adjust based on your computer's RAM and CPU"
            },
            "enhancement": {
                "enabled": True,
                "auto_enhance": True,
                "default_level": "medium",
                "_comment_level": "Options: light, medium, aggressive, auto"
            },
            "quality_thresholds": {
                "min_resolution": [400, 400],
                "_comment_resolution": "Images smaller than this will be upscaled",
                "min_sharpness": 50.0,
                "_comment_sharpness": "Lower values = more aggressive sharpening"
            },
            "performance": {
                "memory_limit_mb": 1024,
                "_comment_memory": "Reduce if you have limited RAM",
                "batch_size": 3,
                "_comment_batch": "Process fewer images at once for slower computers"
            },
            "compatibility": {
                "low_end_mode": False,
                "_comment_low_end": "Enable for computers with limited resources",
                "skip_heavy_operations": False,
                "_comment_skip": "Skip CPU-intensive enhancements"
            }
        }
        
        try:
            with open(output_path, 'w', encoding='utf-8') as f:
                json.dump(template, f, indent=2, ensure_ascii=False)
            print(f"Configuration template created: {output_path}")
            return output_path
        except Exception as e:
            print(f"Error creating configuration template: {e}")
            return None


# Global configuration instance
_config_instance = None

def get_config() -> ImageQualityConfig:
    """Get the global configuration instance."""
    global _config_instance
    if _config_instance is None:
        _config_instance = ImageQualityConfig()
    return _config_instance

def reload_config():
    """Reload the configuration from file."""
    global _config_instance
    _config_instance = None
    return get_config()


# Convenience functions
def is_enhancement_enabled() -> bool:
    """Quick check if enhancement is enabled."""
    return get_config().is_enhancement_enabled()

def get_enhancement_level() -> str:
    """Get the current enhancement level."""
    return get_config().get_enhancement_level()

def is_low_end_mode() -> bool:
    """Check if running in low-end mode."""
    return get_config().is_low_end_mode()