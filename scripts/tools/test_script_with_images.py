import sys
import os
import json
import time

sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from velox_client import VeloxClient

token = os.getenv("VELOX_ADMIN_TOKEN", "d6e31eb8d805b0cc91ef439aae42658b2838531b1de35b804f6932ca439c077d")
client = VeloxClient("http://127.0.0.1:8000", token)

payload = {
    "version": 2,
    "preset": "custom",
    "correlation_id": "test_script_with_images_correlation_roma_v5",
    "items": [
        {
            "id": "test-item-images-roma-v5",
            "title": "Antica Roma",
            "language": "it",
            "tone": "informative",
            "style": "whiteboard",
            "source": {
                "type": "text",
                "topic": "L'Antica Roma: l'ascesa della Repubblica, i grandi imperatori e il Colosseo."
            },
            "output": {
                "generate_scene_images": "enabled",
                "save_to_db": True
            }
        }
    ]
}

print("Submitting script generation request with images...")
try:
    resp = client.submit_async(
        "api/script/generate",
        payload,
        req_id=f"req-img-test-{int(time.time())}"
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
            
            output_file = "test_script_with_images_result.json"
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
