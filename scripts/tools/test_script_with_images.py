import sys
import os
import json
import time

sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from velox_client import VeloxClient

token = os.getenv("VELOX_ADMIN_TOKEN", "")
if not token:
    sys.stderr.write("❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth (or export manually) before running this script.\n")
    sys.exit(2)
image_style = os.getenv("SCRIPT_IMAGE_STYLE", "cinematic")

base_url = os.getenv("VELOX_MASTER_URL")
if not base_url:
    api_base = os.getenv("API_BASE", "127.0.0.1:8080")
    if "://" not in api_base:
        base_url = f"http://{api_base}"
    else:
        base_url = api_base

client = VeloxClient(base_url, token)

payload = {
    "version": 2,
    "preset": "custom",
    "correlation_id": f"test_script_with_images_correlation_roma_v7_{time.time_ns()}",
    "items": [
        {
            "id": "test-item-images-roma-v7",
            "title": "Antica Roma",
            "language": "it",
            "tone": "informative",
            "style": image_style,
            "source": {
                "type": "text",
                "topic": "L'Antica Roma: l'ascesa della Repubblica, i grandi imperatori e il Colosseo."
            },
            "output": {
                "generate_scene_images": "enabled",
                "save_to_db": False
            }
        }
    ]
}

print("Submitting script generation request with images...")
print(f"Using base URL: {base_url}")
print(f"Using image style: {image_style}")
try:
    request_id = f"req-img-test-{time.time_ns()}"
    resp = client.submit_async(
        "api/script/generate",
        payload,
        req_id=request_id
    )
    print("Response received:", json.dumps(resp, indent=2))
    job_id = resp.get("job_id") or resp.get("id")
    if not job_id:
        print("Error: No job_id in response")
        sys.exit(1)
        
    print(f"Waiting for job {job_id} to finish...")
    while True:
        status = client.get_job(job_id)
        current_status = status.get("status")
        print(f"Job status: {current_status}")
        if current_status in ("completed", "failed", "cancelled", "SUCCEEDED", "FAILED"):
            print("Job finished. Final response:")
            print(json.dumps(status, indent=2))

            result = status.get("result") or {}
            output = result.get("output") or {}
            specscene = output.get("specscene") or {}
            scenes = specscene.get("scenes") or []
            print("\nScene image bindings:")
            for scene in scenes:
                bindings = scene.get("bindings") or {}
                image = bindings.get("image") or {}
                print(
                    f"- scene[{scene.get('index', '?')}] id={scene.get('id', '')} "
                    f"text={scene.get('text', '')!r} "
                    f"image_url={image.get('url', '')!r} "
                    f"image_status={image.get('status', '')!r}"
                )

            if scenes and not all((scene.get("bindings") or {}).get("image", {}).get("url") for scene in scenes):
                print("ERROR: One or more scenes are missing image URLs.")
                sys.exit(1)
            
            # Status is also already printed above via
            # json.dumps(status) so the operator sees it on stdout.
            # The on-disk copy is written under /tmp/ so the
            # transient run artifact does not re-enter the working
            # tree (this file was committed in a60de6da5 and deleted
            # by AGENTS.md evidence-dump ratchet in a60e77198).
            # /tmp/test_script_with_images_result.json matches the
            # existing /tmp/*.out gitignore pattern.
            output_file = "/tmp/test_script_with_images_result.json"
            with open(output_file, "w") as f:
                json.dump(status, f, indent=2)
            print(f"Result saved to {output_file}")
            
            if current_status in ("completed", "SUCCEEDED"):
                print("SUCCESS: Script generated successfully!")
            else:
                print("FAILURE: Script generation failed.")
            break
        time.sleep(5)
except Exception as e:
    print(f"An error occurred: {e}")
