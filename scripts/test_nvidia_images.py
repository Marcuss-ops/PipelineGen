import requests
import base64
import os
import sys
import argparse
import yaml

def test_ai_gen(prompt, model, api_key=None, width=1024, height=1024, output_path="test_ai.png"):
    if model == "local-nim":
        invoke_url = "http://localhost:8000/v1/infer"
        payload = {
            "prompt": prompt,
            "mode": "base",
            "seed": 0,
            "steps": 30
        }
        headers = {
            "Accept": "application/json",
            "Content-Type": "application/json"
        }
    elif model == "flux-1-dev":
        invoke_url = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.1-dev"
        payload = {
            "prompt": prompt,
            "mode": "base",
            "cfg_scale": 3.5,
            "width": width,
            "height": height,
            "seed": 0,
            "steps": 50
        }
        headers = {
            "Authorization": f"Bearer {api_key}",
            "Accept": "application/json",
            "Content-Type": "application/json"
        }
    elif model == "flux-2-klein":
        invoke_url = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.2-klein-4b"
        payload = {
            "prompt": prompt,
            "width": width,
            "height": height,
            "seed": 0,
            "steps": 4
        }
        headers = {
            "Authorization": f"Bearer {api_key}",
            "Accept": "application/json",
            "Content-Type": "application/json"
        }
    else:
        print(f"Unsupported model: {model}")
        return False

    print(f"Invoking {model} at {invoke_url} (Resolution: {width}x{height})...")
    try:
        response = requests.post(invoke_url, headers=headers, json=payload)
    except requests.exceptions.ConnectionError:
        print(f"Error: Could not connect to {invoke_url}.")
        return False

    if response.status_code != 200:
        print(f"Error: {response.status_code}")
        print(response.text)
        return False

    response_body = response.json()
    
    # Handle different response formats
    base64_data = response_body.get("image")
    if not base64_data:
        artifacts = response_body.get("artifacts", [])
        if artifacts:
            base64_data = artifacts[0].get("base64")

    if base64_data:
        with open(output_path, "wb") as f:
            f.write(base64.b64decode(base64_data))
        print(f"Image saved to {output_path}")
        return True
    
    print("No image data found in response.")
    print(response_body)
    return False

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Test NVIDIA AI Image Generation Models")
    parser.add_argument("--prompt", type=str, default="A simple coffee shop interior", help="Prompt for image generation")
    parser.add_argument("--model", type=str, default="local-nim", choices=["local-nim", "flux-1-dev", "flux-2-klein"], help="Model to use")
    parser.add_argument("--width", type=int, default=1024, help="Image width")
    parser.add_argument("--height", type=int, default=1024, help="Image height")
    parser.add_argument("--output", type=str, default="test_ai.png", help="Output file path")
    parser.add_argument("--config", type=str, default="config.yaml", help="Path to config.yaml for API key")
    
    args = parser.parse_args()
    
    api_key = None
    if args.model != "local-nim":
        if os.path.exists(args.config):
            with open(args.config, 'r') as f:
                cfg = yaml.safe_load(f)
                api_key = cfg.get('external', {}).get('nvidia_api_key')
        
        if not api_key or api_key == "PASTE_YOUR_NVIDIA_API_KEY_HERE":
            print("Error: NVIDIA API Key not found in config.yaml. Required for cloud models.")
            sys.exit(1)
            
    success = test_ai_gen(args.prompt, args.model, api_key, args.width, args.height, args.output)
    if not success:
        sys.exit(1)
