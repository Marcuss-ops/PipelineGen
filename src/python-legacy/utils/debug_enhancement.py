#!/usr/bin/env python3
"""Debug script to test image enhancement."""

import sys
import os
from pathlib import Path
import tempfile
from PIL import Image

# Aggiungi la root del progetto al path
project_root = Path(__file__).parent.parent.parent
sys.path.insert(0, str(project_root))

try:
    from modules.video.image_quality_enhancer import ImageQualityEnhancer
except ImportError:
    try:
        from image_quality_enhancer import ImageQualityEnhancer  # type: ignore
    except ImportError:
        sys.path.insert(0, os.path.dirname(__file__))
        from image_quality_enhancer import ImageQualityEnhancer  # type: ignore

def create_test_image():
    """Create a simple test image."""
    img = Image.new('RGB', (100, 100), color='red')
    temp_file = tempfile.NamedTemporaryFile(suffix='.jpg', delete=False)
    img.save(temp_file.name)
    return temp_file.name

def debug_enhancement():
    """Debug image enhancement."""
    print("Testing image enhancement...")
    
    # Create test image
    test_image = create_test_image()
    print(f"Created test image: {test_image}")
    
    try:
        # Create enhancer
        enhancer = ImageQualityEnhancer()
        print(f"Created enhancer successfully")
        
        # Test assess_image_quality
        print("Testing assess_image_quality...")
        quality_info = enhancer.assess_image_quality(test_image)
        print(f"Quality info type: {type(quality_info)}")
        print(f"Quality info: {quality_info}")
        
        # Test enhance_image
        print("Testing enhance_image...")
        enhanced_path = enhancer.enhance_image(test_image)
        print(f"Enhanced path: {enhanced_path}")
        
    except Exception as e:
        print(f"Error during enhancement: {e}")
        import traceback
        traceback.print_exc()
    
    finally:
        # Clean up
        if os.path.exists(test_image):
            os.unlink(test_image)

if __name__ == "__main__":
    debug_enhancement()