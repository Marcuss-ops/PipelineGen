from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Callable, TypeVar, List, Any
import logging

logger = logging.getLogger("StockProcessor")

T = TypeVar('T')
R = TypeVar('R')

def parallel_map(
    func: Callable[[T], R],
    items: List[T],
    max_workers: int = 3,
    description: str = "Processing"
) -> List[R]:
    results: List[R] = []
    failed = []
    
    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        future_to_item = {executor.submit(func, item): item for item in items}
        
        for future in as_completed(future_to_item):
            item = future_to_item[future]
            try:
                result = future.result()
                results.append(result)
                print(f"[{len(results)}/{len(items)}] Done: {item}")
            except Exception as e:
                failed.append((item, str(e)))
                logger.error(f"Failed {item}: {e}")
    
    if failed:
        logger.warning(f"{len(failed)} items failed in {description}")
    
    return results

def parallel_download(
    urls: List[str],
    download_func: Callable[[str], Any],
    max_workers: int = 3
) -> List:
    logger.info(f"Starting parallel download of {len(urls)} URLs (max_workers={max_workers})")
    
    results = parallel_map(
        download_func,
        urls,
        max_workers=max_workers,
        description="Download"
    )
    
    successful = [r for r in results if r is not None]
    logger.info(f"Download complete: {len(successful)}/{len(urls)} successful")
    
    return successful
