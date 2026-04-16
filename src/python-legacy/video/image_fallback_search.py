"""
Image Fallback Search Module

This module provides functionality to search for alternative images when the original
downloaded images are of poor quality. It uses multiple search strategies and sources
to find better quality images.

Designed for CPU-only environments without GPU dependencies.
"""

import os
import logging
import requests
from typing import List, Optional, Dict, Tuple
from urllib.parse import quote_plus, urljoin
import time
import random
from PIL import Image
import json

# Configure logging
logger = logging.getLogger(__name__)

class ImageFallbackSearcher:
    """
    Searches for alternative images when original images are of poor quality.
    Uses multiple search engines and strategies to find better quality images.
    """
    
    def __init__(self):
        self.session = requests.Session()
        self.session.headers.update({
            'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36'
        })
        
        # Search engines and their query patterns
        self.search_engines = {
            'duckduckgo': {
                'base_url': 'https://duckduckgo.com/',
                'requires_api': False,
                'quality': 'medium'
            },
            'unsplash': {
                'base_url': 'https://source.unsplash.com/800x600/?',
                'requires_api': False,
                'quality': 'high'
            },
            'pixabay': {
                'base_url': 'https://pixabay.com/api/',
                'requires_api': True,
                'quality': 'high'
            },
            'pexels': {
                'base_url': 'https://api.pexels.com/v1/search',
                'requires_api': True,
                'quality': 'high'
            }
        }
        
        # Minimum quality requirements
        self.min_resolution = (400, 300)  # Minimum width x height
        self.min_file_size = 10000  # Minimum file size in bytes
        
    def search_alternative_images(self, entity_name: str, max_results: int = 5) -> List[str]:
        """
        Search for alternative images for the given entity.
        
        Args:
            entity_name: Name of the entity to search images for
            max_results: Maximum number of alternative images to return
            
        Returns:
            List of image URLs found
        """
        logger.info(f"Searching alternative images for: {entity_name}")
        
        alternative_urls = []
        
        # Clean and prepare search terms
        search_terms = self._prepare_search_terms(entity_name)
        
        # Try DuckDuckGo first (better for entities/people)
        for search_term in search_terms[:2]:
            duckduckgo_urls = self._search_duckduckgo(search_term, max_results=3)
            alternative_urls.extend(duckduckgo_urls)
            if len(alternative_urls) >= max_results:
                break
            time.sleep(0.3)
        
        # If not enough, try Unsplash
        if len(alternative_urls) < max_results:
            for search_term in search_terms[:2]:
                urls = self._search_unsplash(search_term, max_results=2)
                alternative_urls.extend(urls)
                if len(alternative_urls) >= max_results:
                    break
                time.sleep(0.3)
        
        # Remove duplicates and limit results
        unique_urls = list(dict.fromkeys(alternative_urls))[:max_results]
        
        logger.info(f"Found {len(unique_urls)} alternative images for {entity_name}")
        return unique_urls
    
    def _prepare_search_terms(self, entity_name: str) -> List[str]:
        """
        Prepare different variations of search terms for better results.
        
        Args:
            entity_name: Original entity name
            
        Returns:
            List of search term variations
        """
        terms = []
        
        # Original term
        clean_name = entity_name.strip().lower()
        terms.append(clean_name)
        
        # Remove common words that might interfere with search
        stop_words = ['the', 'a', 'an', 'and', 'or', 'but', 'in', 'on', 'at', 'to', 'for', 'of', 'with', 'by']
        words = clean_name.split()
        filtered_words = [word for word in words if word not in stop_words]
        
        if len(filtered_words) > 0 and len(filtered_words) != len(words):
            terms.append(' '.join(filtered_words))
        
        # If multiple words, try just the first significant word
        if len(filtered_words) > 1:
            terms.append(filtered_words[0])
        
        # Add generic terms for better visual results
        if len(filtered_words) > 0:
            terms.append(f"{filtered_words[0]} professional")
            terms.append(f"{filtered_words[0]} high quality")
        
        return terms
    
    def _search_unsplash(self, search_term: str, max_results: int = 3) -> List[str]:
        """
        Search for images on Unsplash (free, no API key required).
        
        Args:
            search_term: Term to search for
            max_results: Maximum number of results
            
        Returns:
            List of image URLs
        """
        urls = []
        
        try:
            # Unsplash Source API - simple and reliable
            base_url = "https://source.unsplash.com"
            
            # Different size variations for better quality
            sizes = ["800x600", "1024x768", "1200x800"]
            
            for i in range(min(max_results, len(sizes))):
                size = sizes[i]
                # Add random seed to get different images
                seed = random.randint(1000, 9999)
                url = f"{base_url}/{size}/?{quote_plus(search_term)}&sig={seed}"
                urls.append(url)
                
        except Exception as e:
            logger.error(f"Error searching Unsplash for '{search_term}': {e}")
        
        return urls

    def _search_duckduckgo(self, search_term: str, max_results: int = 3) -> List[str]:
        """
        Search for images on DuckDuckGo using lite API.
        
        Args:
            search_term: Term to search for
            max_results: Maximum number of results
            
        Returns:
            List of image URLs
        """
        urls = []
        
        try:
            response = self.session.get(
                f"https://lite.duckduckgo.com/lite/?q={quote_plus(search_term)}",
                timeout=10
            )
            if response.status_code == 200:
                from bs4 import BeautifulSoup
                soup = BeautifulSoup(response.text, 'html.parser')
                for result in soup.select('a.result__a')[:max_results]:
                    href = result.get('href', '')
                    if 'uddg=' in href:
                        from urllib.parse import unquote
                        img_url = unquote(href.split('uddg=')[1].split('&')[0])
                        if img_url.startswith('http'):
                            urls.append(img_url)
        except Exception as e:
            logger.error(f"DuckDuckGo lite search failed: {e}")
        
        return urls[:max_results]
    
    def _search_bing_images(self, search_term: str, max_results: int = 3) -> List[str]:
        """Fallback to Bing Images API."""
        urls = []
        try:
            Bing_API_Key = os.environ.get("BING_API_KEY", "")
            if Bing_API_Key:
                endpoint = "https://api.bing.microsoft.com/v7.0/images/search"
                response = self.session.get(
                    endpoint,
                    params={"q": search_term, "count": max_results},
                    headers={"Ocp-Apim-Subscription-Key": Bing_API_Key},
                    timeout=10
                )
                if response.status_code == 200:
                    for item in response.json().get("value", [])[:max_results]:
                        if item.get("contentUrl"):
                            urls.append(item["contentUrl"])
        except Exception as e:
            logger.warning(f"Bing search failed: {e}")
        return urls
        
        return urls
    
    def validate_alternative_image(self, image_url: str, temp_path: str) -> Tuple[bool, Dict]:
        """
        Download and validate an alternative image to ensure it meets quality requirements.
        
        Args:
            image_url: URL of the image to validate
            temp_path: Temporary path to save the image for validation
            
        Returns:
            Tuple of (is_valid, quality_info)
        """
        try:
            # Download the image
            response = self.session.get(image_url, timeout=10, stream=True)
            response.raise_for_status()
            
            # Check content type
            content_type = response.headers.get('content-type', '').lower()
            if not any(img_type in content_type for img_type in ['image/jpeg', 'image/png', 'image/webp']):
                return False, {'error': 'Invalid content type'}
            
            # Save temporarily
            with open(temp_path, 'wb') as f:
                for chunk in response.iter_content(chunk_size=8192):
                    f.write(chunk)
            
            # Check file size
            file_size = os.path.getsize(temp_path)
            if file_size < self.min_file_size:
                os.remove(temp_path)
                return False, {'error': 'File too small', 'size': file_size}
            
            # Validate with PIL
            with Image.open(temp_path) as img:
                img.verify()
                
                # Re-open for size check (verify() closes the image)
                with Image.open(temp_path) as img2:
                    width, height = img2.size
                    
                    # Check minimum resolution
                    if width < self.min_resolution[0] or height < self.min_resolution[1]:
                        os.remove(temp_path)
                        return False, {'error': 'Resolution too low', 'resolution': (width, height)}
                    
                    quality_info = {
                        'resolution': (width, height),
                        'file_size': file_size,
                        'format': img2.format,
                        'mode': img2.mode
                    }
                    
                    return True, quality_info
                    
        except Exception as e:
            logger.error(f"Error validating alternative image {image_url}: {e}")
            if os.path.exists(temp_path):
                try:
                    os.remove(temp_path)
                except:
                    pass
            return False, {'error': str(e)}
    
    def find_best_alternative(self, entity_name: str, output_path: str) -> Optional[str]:
        """
        Find and download the best alternative image for an entity.
        
        Args:
            entity_name: Name of the entity
            output_path: Path where to save the best alternative image
            
        Returns:
            Path to the downloaded image if successful, None otherwise
        """
        logger.info(f"Finding best alternative image for: {entity_name}")
        
        # Search for alternatives
        alternative_urls = self.search_alternative_images(entity_name, max_results=5)
        
        if not alternative_urls:
            logger.warning(f"No alternative images found for {entity_name}")
            return None
        
        # Test each alternative
        temp_dir = os.path.dirname(output_path)
        best_image = None
        best_quality_score = 0
        
        for i, url in enumerate(alternative_urls):
            temp_path = os.path.join(temp_dir, f"temp_alt_{i}.jpg")
            
            try:
                is_valid, quality_info = self.validate_alternative_image(url, temp_path)
                
                if is_valid:
                    # Calculate quality score
                    width, height = quality_info['resolution']
                    file_size = quality_info['file_size']
                    
                    # Simple quality scoring based on resolution and file size
                    resolution_score = (width * height) / (1024 * 768)  # Normalize to 1024x768
                    size_score = min(file_size / 100000, 2.0)  # Normalize file size
                    
                    quality_score = resolution_score * size_score
                    
                    logger.info(f"Alternative {i+1}: Quality score {quality_score:.2f} "
                              f"({width}x{height}, {file_size} bytes)")
                    
                    if quality_score > best_quality_score:
                        # Remove previous best if exists
                        if best_image and os.path.exists(best_image):
                            os.remove(best_image)
                        
                        best_image = temp_path
                        best_quality_score = quality_score
                    else:
                        # Remove this temp file
                        os.remove(temp_path)
                else:
                    logger.warning(f"Alternative {i+1} failed validation: {quality_info.get('error', 'Unknown error')}")
                    
            except Exception as e:
                logger.error(f"Error processing alternative {i+1}: {e}")
                if os.path.exists(temp_path):
                    try:
                        os.remove(temp_path)
                    except:
                        pass
            
            # Small delay between downloads
            time.sleep(0.3)
        
        # Move best image to final location
        if best_image and os.path.exists(best_image):
            try:
                if os.path.exists(output_path):
                    os.remove(output_path)
                os.rename(best_image, output_path)
                logger.info(f"Best alternative image saved: {output_path} (score: {best_quality_score:.2f})")
                return output_path
            except Exception as e:
                logger.error(f"Error moving best alternative image: {e}")
                return None
        
        logger.warning(f"No suitable alternative image found for {entity_name}")
        return None


# Convenience functions for easy integration
def search_alternative_image(entity_name: str, output_path: str) -> Optional[str]:
    """
    Convenience function to search for and download an alternative image.
    
    Args:
        entity_name: Name of the entity to search for
        output_path: Path where to save the image
        
    Returns:
        Path to downloaded image if successful, None otherwise
    """
    searcher = ImageFallbackSearcher()
    return searcher.find_best_alternative(entity_name, output_path)


def should_search_alternative(image_path: str, min_quality_score: float = 0.3) -> bool:
    """
    Determine if we should search for an alternative image based on current image quality.
    
    Args:
        image_path: Path to the current image
        min_quality_score: Minimum quality score threshold
        
    Returns:
        True if alternative search is recommended
    """
    try:
        # Import with fallback
        try:
            from .image_quality_enhancer import assess_image_quality
        except ImportError:
            from image_quality_enhancer import assess_image_quality
        
        quality_score, _ = assess_image_quality(image_path)
        return quality_score < min_quality_score
        
    except Exception as e:
        logger.error(f"Error assessing image quality for fallback decision: {e}")
        return False