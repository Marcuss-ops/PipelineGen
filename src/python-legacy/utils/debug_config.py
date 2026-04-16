#!/usr/bin/env python3
"""Debug script to test configuration loading."""

import sys
import os
from pathlib import Path

# Aggiungi la root del progetto al path
project_root = Path(__file__).parent.parent.parent
sys.path.insert(0, str(project_root))

try:
    from modules.video.image_quality_config import get_config
    from modules.video.image_quality_enhancer import ImageQualityEnhancer
except ImportError:
    try:
        from image_quality_config import get_config  # type: ignore
        from image_quality_enhancer import ImageQualityEnhancer  # type: ignore
    except ImportError:
        # Fallback: aggiungi la directory video al path
        video_dir = project_root / "modules" / "video"
        if str(video_dir) not in sys.path:
            sys.path.insert(0, str(video_dir))
        from image_quality_config import get_config  # type: ignore
        from image_quality_enhancer import ImageQualityEnhancer  # type: ignore

def debug_config():
    """Debug configuration loading."""
    print("Testing configuration loading...")
    
    # Test get_config function
    config = get_config()
    print(f"Config type: {type(config)}")
    print(f"Config has get method: {hasattr(config, 'get')}")
    
    # Test config methods
    try:
        enhancement_enabled = config.is_enhancement_enabled()
        print(f"Enhancement enabled: {enhancement_enabled}")
    except Exception as e:
        print(f"Error checking enhancement enabled: {e}")
    
    try:
        performance_settings = config.get_performance_settings()
        print(f"Performance settings type: {type(performance_settings)}")
        print(f"Performance settings: {performance_settings}")
    except Exception as e:
        print(f"Error getting performance settings: {e}")
    
    try:
        quality_thresholds = config.get_quality_thresholds()
        print(f"Quality thresholds type: {type(quality_thresholds)}")
        print(f"Quality thresholds: {quality_thresholds}")
    except Exception as e:
        print(f"Error getting quality thresholds: {e}")
    
    # Test ImageQualityEnhancer initialization
    try:
        enhancer = ImageQualityEnhancer()
        print(f"Enhancer config type: {type(enhancer.config)}")
        print(f"Enhancer config has get method: {hasattr(enhancer.config, 'get')}")
        
        # Test the specific call that's failing
        max_time = enhancer.config.get('enhancement.max_processing_time', 30)
        print(f"Max processing time: {max_time}")
        
    except Exception as e:
        print(f"Error creating enhancer: {e}")
        import traceback
        traceback.print_exc()

if __name__ == "__main__":
    debug_config()