import sys
import json
import argparse
import os
from TikTokApi import TikTokApi
import asyncio

async def search_videos(query, count=10, search_type="general"):
    """
    Cerca video su TikTok usando l'API di David Teather.
    search_type: 'general', 'hashtag', 'user'
    """
    results = []
    async with TikTokApi() as api:
        await api.create_sessions(ms_tokens=["ms_token_placeholder"], num_sessions=1, sleep_after=3)
        
        if search_type == "hashtag":
            tag = await api.hashtag(name=query)
            async for video in tag.videos(count=count):
                results.append(format_video(video))
        elif search_type == "user":
            user = await api.user(username=query)
            async for video in user.videos(count=count):
                results.append(format_video(video))
        else:
            async for video in api.search.videos(query, count=count):
                results.append(format_video(video))
                
    return results

def format_video(video):
    """Formatta l'oggetto video dell'API nel nostro formato standard."""
    return {
        "id": video.id,
        "title": video.desc,
        "description": video.desc,
        "author": video.author.username,
        "url": f"https://www.tiktok.com/@{video.author.username}/video/{video.id}",
        "duration": video.stats.get("duration", 0),
        "views": video.stats.get("playCount", 0),
        "digg_count": video.stats.get("diggCount", 0),
        "collect_count": video.stats.get("collectCount", 0),
        "comment_count": video.stats.get("commentCount", 0),
        "share_count": video.stats.get("shareCount", 0),
        "create_time": video.create_time.isoformat() if hasattr(video, 'create_time') else None,
    }

async def download_video(video_url, output_path):
    """Scarica un video specifico."""
    async with TikTokApi() as api:
        await api.create_sessions(ms_tokens=["ms_token_placeholder"], num_sessions=1, sleep_after=3)
        video = api.video(url=video_url)
        video_data = await video.bytes()
        
        with open(output_path, "wb") as f:
            f.write(video_data)
        
        return output_path

async def main():
    parser = argparse.ArgumentParser(description="TikTok Downloader via TikTok-Api")
    parser.add_argument("--action", choices=["search", "download", "info"], required=True)
    parser.add_argument("--query", help="Query di ricerca o URL video")
    parser.add_argument("--type", choices=["general", "hashtag", "user"], default="general")
    parser.add_argument("--count", type=int, default=10)
    parser.add_argument("--output", help="Percorso di output per il download")
    
    args = parser.parse_args()

    try:
        if args.action == "search":
            results = await search_videos(args.query, args.count, args.type)
            print(json.dumps(results))
        elif args.action == "download":
            if not args.output:
                print(json.dumps({"error": "Percorso output richiesto per download"}))
                return
            path = await download_video(args.query, args.output)
            print(json.dumps({"status": "success", "path": path}))
        elif args.action == "info":
            async with TikTokApi() as api:
                await api.create_sessions(ms_tokens=["ms_token_placeholder"], num_sessions=1, sleep_after=3)
                video = api.video(url=args.query)
                video_info = await video.info()
                print(json.dumps(video_info))
    except Exception as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)

if __name__ == "__main__":
    asyncio.run(main())
