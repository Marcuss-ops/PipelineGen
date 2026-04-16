"""CPU-based Image Quality Enhancement Module
Provides image quality assessment and enhancement without GPU requirements.
"""

import os
import logging
import time
from typing import List, Tuple, Dict, Optional, Union, Any
from PIL import Image, ImageFilter, ImageEnhance
import numpy as np

# Import configuration with fallback
try:
    from .image_quality_config import get_config
except ImportError:
    from image_quality_config import get_config

logger = logging.getLogger(__name__)


class ImageQualityEnhancer:
    """CPU-based image quality enhancer with configurable settings."""
    
    def __init__(self):
        self.config = get_config()
        self.performance_settings = self.config.get_performance_settings()
        self.quality_thresholds = self.config.get_quality_thresholds()
    
    def assess_image_quality(self, image_path: str) -> Dict[str, Union[float, bool, List[str]]]:
        """
        Assess image quality using multiple CPU-based metrics.
        
        Args:
            image_path: Path to the image file
            
        Returns:
            Dictionary containing quality metrics and enhancement suggestions
        """
        try:
            # Check file size first for performance
            file_size_mb = os.path.getsize(image_path) / (1024 * 1024)
            max_size = self.config.get('enhancement.max_size_mb', 50)
            
            if self.config.get('enhancement.skip_large_images', True) and file_size_mb > max_size:
                return {
                    'overall_score': 0.8,  # Assume large images are decent quality
                    'skipped': True,
                    'reason': f'File too large ({file_size_mb:.1f}MB > {max_size}MB)',
                    'suggestions': []
                }
            
            with Image.open(image_path) as img:
                # Convert to RGB if necessary
                if img.mode != 'RGB':
                    img = img.convert('RGB')
                
                # Get image dimensions
                width, height = img.size
                
                # Calculate metrics based on configuration
                metrics = {}
                
                # Resolution assessment
                min_res = self.quality_thresholds.get('min_resolution', [400, 400])
                target_res = self.quality_thresholds.get('target_resolution', [1920, 1080])
                
                resolution_score = min(width / target_res[0], height / target_res[1], 1.0)
                metrics['resolution_score'] = resolution_score
                metrics['dimensions'] = [width, height]
                
                # Skip heavy operations in low-end mode
                if self.config.is_low_end_mode():
                    overall_score = resolution_score
                    suggestions = []
                    if resolution_score < self.quality_thresholds.get('min_resolution_score', 0.3):
                        suggestions.append('upscale')
                else:
                    # Full quality assessment
                    img_array = np.array(img)
                    
                    # Sharpness (Laplacian variance)
                    gray = np.dot(img_array[...,:3], [0.2989, 0.5870, 0.1140])
                    laplacian_var = self._calculate_laplacian_variance(gray)
                    metrics['sharpness'] = laplacian_var
                    
                    # Contrast (standard deviation)
                    contrast = np.std(gray)
                    metrics['contrast'] = contrast
                    
                    # Brightness variance
                    brightness_var = np.var(np.mean(img_array, axis=2))
                    metrics['brightness_variance'] = brightness_var
                    
                    # Color richness (unique colors ratio)
                    unique_colors = len(np.unique(img_array.reshape(-1, img_array.shape[-1]), axis=0))
                    total_pixels = width * height
                    color_richness = min(unique_colors / total_pixels, 1.0)
                    metrics['color_richness'] = color_richness
                    
                    # Calculate overall score
                    scores = [
                        resolution_score,
                        min(laplacian_var / 100.0, 1.0),  # Normalize sharpness
                        min(contrast / 100.0, 1.0),       # Normalize contrast
                        min(brightness_var / 100.0, 1.0), # Normalize brightness
                        color_richness
                    ]
                    overall_score = np.mean(scores)
                    
                    # Generate suggestions based on thresholds
                    suggestions = []
                    if resolution_score < self.quality_thresholds.get('min_resolution_score', 0.3):
                        suggestions.append('upscale')
                    if laplacian_var < self.quality_thresholds.get('min_sharpness', 50.0):
                        suggestions.append('sharpen')
                    if contrast < self.quality_thresholds.get('min_contrast', 30.0):
                        suggestions.append('enhance_contrast')
                    if brightness_var < self.quality_thresholds.get('min_brightness_variance', 20.0):
                        suggestions.append('adjust_brightness')
                
                return {
                    'overall_score': overall_score,
                    'metrics': metrics,
                    'suggestions': suggestions,
                    'needs_enhancement': overall_score < 0.6,
                    'file_size_mb': file_size_mb
                }
                
        except Exception as e:
            logger.error(f"Error assessing image quality for {image_path}: {e}")
            return {
                'overall_score': 0.5,
                'error': str(e),
                'suggestions': [],
                'needs_enhancement': False
            }
    
    def _calculate_laplacian_variance(self, gray_image: np.ndarray) -> float:
        """Calculate Laplacian variance for sharpness assessment."""
        # Numpy-based implementation of Laplacian filter
        kernel = np.array([[0, -1, 0], [-1, 4, -1], [0, -1, 0]])
        try:
            from scipy import ndimage
            laplacian = ndimage.convolve(gray_image, kernel)
        except ImportError:
            # Simple convolution fallback without scipy
            h, w = gray_image.shape
            laplacian = np.zeros_like(gray_image)
            for i in range(1, h-1):
                for j in range(1, w-1):
                    laplacian[i, j] = (4 * gray_image[i, j] - 
                                     gray_image[i-1, j] - gray_image[i+1, j] - 
                                     gray_image[i, j-1] - gray_image[i, j+1])
        return np.var(laplacian)
    
    def _get_enhancement_suggestions(self, quality_scores: Dict[str, float]) -> list:
        """Get specific enhancement suggestions based on quality scores."""
        suggestions = []
        
        if quality_scores['resolution_score'] < self.quality_thresholds.get('resolution_score', 0.3):
            suggestions.append('upscale')
        
        if quality_scores['sharpness'] < self.quality_thresholds.get('sharpness', 50.0):
            suggestions.append('sharpen')
        
        if quality_scores['contrast'] < self.quality_thresholds.get('contrast', 30.0):
            suggestions.append('enhance_contrast')
        
        if quality_scores['brightness_variance'] < self.quality_thresholds.get('brightness', 20.0):
            suggestions.append('adjust_brightness')
        
        return suggestions
    
    def enhance_image(self, image_path: str, output_path: str = None, 
                     enhancement_level: str = None) -> Optional[str]:
        """
        Enhance image quality using CPU-based algorithms.
        
        Args:
            image_path: Path to input image
            output_path: Path for enhanced image (optional)
            enhancement_level: 'light', 'medium', 'aggressive', or 'auto'
            
        Returns:
            Path to enhanced image or None if enhancement failed
        """
        if not self.config.is_enhancement_enabled():
            logger.info("Image enhancement is disabled in configuration")
            return image_path
        
        try:
            start_time = time.time()
            max_time = self.config.get('enhancement.max_processing_time', 30)
            
            # Determine enhancement level
            if enhancement_level is None:
                enhancement_level = self.config.get_enhancement_level()
            
            if enhancement_level == 'auto':
                # Assess image and determine appropriate level
                assessment = self.assess_image_quality(image_path)
                if assessment.get('skipped', False):
                    return image_path
                
                score = assessment.get('overall_score', 0.5)
                if score > 0.7:
                    enhancement_level = 'light'
                elif score > 0.4:
                    enhancement_level = 'medium'
                else:
                    enhancement_level = 'aggressive'
            
            # Get enhancement settings
            enhancement_settings = self.config.get(f'enhancement_levels.{enhancement_level}', {})
            if not enhancement_settings:
                logger.warning(f"Unknown enhancement level: {enhancement_level}")
                return image_path
            
            # Set output path
            if output_path is None:
                base, ext = os.path.splitext(image_path)
                output_path = f"{base}_enhanced{ext}"
            
            with Image.open(image_path) as img:
                # Convert to RGB if necessary
                if img.mode != 'RGB':
                    img = img.convert('RGB')
                
                enhanced_img = img.copy()
                
                # Check processing time limit
                def check_time_limit():
                    if time.time() - start_time > max_time:
                        raise TimeoutError(f"Enhancement timeout ({max_time}s)")
                
                # Apply enhancements based on level and compatibility settings
                skip_heavy = self.config.get('compatibility.skip_heavy_operations', False)
                
                # 1. Upscaling (if needed and not skipped)
                upscale_factor = enhancement_settings.get('upscale_factor', 1.0)
                if upscale_factor > 1.0 and not skip_heavy:
                    check_time_limit()
                    enhanced_img = self._upscale_image(enhanced_img, upscale_factor)
                
                # 2. Sharpening
                sharpness_factor = enhancement_settings.get('sharpness_factor', 1.0)
                if sharpness_factor > 1.0:
                    check_time_limit()
                    enhanced_img = self._sharpen_image(enhanced_img, sharpness_factor)
                
                # 3. Contrast enhancement
                contrast_factor = enhancement_settings.get('contrast_factor', 1.0)
                if contrast_factor != 1.0:
                    check_time_limit()
                    enhanced_img = self._enhance_contrast(enhanced_img, contrast_factor)
                
                # 4. Brightness and color adjustment
                brightness_factor = enhancement_settings.get('brightness_factor', 1.0)
                color_factor = enhancement_settings.get('color_factor', 1.0)
                if brightness_factor != 1.0 or color_factor != 1.0:
                    check_time_limit()
                    enhanced_img = self._adjust_brightness_and_color(
                        enhanced_img, brightness_factor, color_factor)
                
                # 5. Noise reduction (for aggressive mode and if not skipped)
                if (enhancement_settings.get('noise_reduction', False) and 
                    not skip_heavy and not self.config.is_low_end_mode()):
                    check_time_limit()
                    enhanced_img = self._reduce_noise(enhanced_img)
                
                # Save enhanced image
                enhanced_img.save(output_path, quality=95, optimize=True)
                
                processing_time = time.time() - start_time
                logger.info(f"Enhanced image in {processing_time:.2f}s: {image_path} -> {output_path}")
                
                return output_path
                
        except TimeoutError as e:
            logger.warning(f"Enhancement timeout for {image_path}: {e}")
            if self.config.get('compatibility.fallback_to_basic', True):
                return self._basic_enhancement(image_path, output_path)
            return image_path
            
        except Exception as e:
            logger.error(f"Error enhancing image {image_path}: {e}")
            if self.config.get('compatibility.fallback_to_basic', True):
                return self._basic_enhancement(image_path, output_path)
            return image_path
    
    def _upscale_image(self, img: Image.Image, scale_factor: float = 2.0) -> Image.Image:
        """Upscale image using high-quality CPU-based interpolation."""
        current_width, current_height = img.size
        
        # Calculate new size
        new_width = int(current_width * scale_factor)
        new_height = int(current_height * scale_factor)
        
        # Use LANCZOS for high-quality upscaling
        return img.resize((new_width, new_height), Image.LANCZOS)
    
    def _sharpen_image(self, img: Image.Image, sharpness_factor: float) -> Image.Image:
        """Apply sharpening filter based on sharpness factor."""
        if sharpness_factor <= 1.0:
            return img
        
        # Convert factor to UnsharpMask parameters
        if sharpness_factor <= 1.1:
            # Light sharpening
            return img.filter(ImageFilter.UnsharpMask(radius=1, percent=120, threshold=3))
        elif sharpness_factor <= 1.2:
            # Medium sharpening
            return img.filter(ImageFilter.UnsharpMask(radius=1.5, percent=150, threshold=2))
        else:
            # Aggressive sharpening
            sharpened = img.filter(ImageFilter.UnsharpMask(radius=2, percent=180, threshold=1))
            # Apply additional edge enhancement for very high factors
            if sharpness_factor > 1.25:
                sharpened = sharpened.filter(ImageFilter.EDGE_ENHANCE)
            return sharpened
    
    def _enhance_contrast(self, img: Image.Image, contrast_factor: float) -> Image.Image:
        """Enhance image contrast based on factor."""
        if contrast_factor == 1.0:
            return img
        
        enhancer = ImageEnhance.Contrast(img)
        return enhancer.enhance(contrast_factor)
    
    def _adjust_brightness_and_color(self, img: Image.Image, 
                                   brightness_factor: float, color_factor: float) -> Image.Image:
        """Adjust brightness and color saturation."""
        # Enhance brightness
        if brightness_factor != 1.0:
            brightness_enhancer = ImageEnhance.Brightness(img)
            img = brightness_enhancer.enhance(brightness_factor)
        
        # Enhance color saturation
        if color_factor != 1.0:
            color_enhancer = ImageEnhance.Color(img)
            img = color_enhancer.enhance(color_factor)
        
        return img
    
    def _reduce_noise(self, img: Image.Image) -> Image.Image:
        """Apply noise reduction using PIL filters."""
        # Apply median filter for noise reduction
        img = img.filter(ImageFilter.MedianFilter(size=3))
        # Apply slight blur to reduce remaining noise
        img = img.filter(ImageFilter.GaussianBlur(radius=0.5))
        return img
    
    def _basic_enhancement(self, image_path: str, output_path: str = None) -> Optional[str]:
        """
        Basic enhancement fallback for low-end computers or when main enhancement fails.
        Uses minimal CPU resources.
        """
        try:
            if output_path is None:
                base, ext = os.path.splitext(image_path)
                output_path = f"{base}_basic_enhanced{ext}"
            
            with Image.open(image_path) as img:
                if img.mode != 'RGB':
                    img = img.convert('RGB')
                
                # Only apply very light enhancements
                enhanced_img = img.copy()
                
                # Light sharpening only
                enhanced_img = enhanced_img.filter(ImageFilter.UnsharpMask(radius=1, percent=110, threshold=5))
                
                # Very light contrast enhancement
                contrast_enhancer = ImageEnhance.Contrast(enhanced_img)
                enhanced_img = contrast_enhancer.enhance(1.05)
                
                # Save with good compression
                enhanced_img.save(output_path, quality=90, optimize=True)
                logger.info(f"Applied basic enhancement: {image_path} -> {output_path}")
                
                return output_path
                
        except Exception as e:
            logger.error(f"Basic enhancement failed for {image_path}: {e}")
            return image_path
    
    def batch_enhance_images(self, image_paths: list, output_dir: str, 
                           enhancement_level: str = 'auto') -> Dict[str, Any]:
        """
        Enhance multiple images in batch.
        
        Args:
            image_paths: List of input image paths
            output_dir: Directory for enhanced images
            enhancement_level: Enhancement level to apply
            
        Returns:
            Dictionary with batch processing results
        """
        os.makedirs(output_dir, exist_ok=True)
        
        results = {
            'total_images': len(image_paths),
            'successful': 0,
            'failed': 0,
            'results': [],
            'average_improvement': 0
        }
        
        total_improvement = 0
        
        for i, image_path in enumerate(image_paths):
            try:
                filename = os.path.basename(image_path)
                name, ext = os.path.splitext(filename)
                output_path = os.path.join(output_dir, f"{name}_enhanced{ext}")
                
                result = self.enhance_image(image_path, output_path, enhancement_level)
                
                if result.get('success', False):
                    results['successful'] += 1
                    total_improvement += result.get('improvement', 0)
                else:
                    results['failed'] += 1
                
                results['results'].append({
                    'input_path': image_path,
                    'output_path': output_path if result.get('success', False) else None,
                    'result': result
                })
                
                # Log progress
                logger.info(f"Processed {i+1}/{len(image_paths)}: {filename}")
                
            except Exception as e:
                logger.error(f"Error processing {image_path}: {e}")
                results['failed'] += 1
                results['results'].append({
                    'input_path': image_path,
                    'output_path': None,
                    'result': {'success': False, 'error': str(e)}
                })
        
        if results['successful'] > 0:
            results['average_improvement'] = total_improvement / results['successful']
        
        return results


# Convenience functions for easy integration
def assess_image_quality(image_path: str) -> Dict[str, Any]:
    """Quick function to assess image quality."""
    enhancer = ImageQualityEnhancer()
    return enhancer.assess_image_quality(image_path)


def enhance_image_quality(image_path: str, output_path: str, 
                         level: str = 'auto') -> Dict[str, Any]:
    """Quick function to enhance image quality."""
    enhancer = ImageQualityEnhancer()
    return enhancer.enhance_image(image_path, output_path, level)


def should_enhance_image(image_path: str) -> bool:
    """Quick check if an image needs enhancement."""
    quality_assessment = assess_image_quality(image_path)
    return quality_assessment.get('needs_enhancement', True)