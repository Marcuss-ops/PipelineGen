#!/usr/bin/env python3
import os
import json
import argparse
from pathlib import Path
from google_auth_oauthlib.flow import InstalledAppFlow
from google.auth.transport.requests import Request
from google.oauth2.credentials import Credentials

ROOT = Path(__file__).resolve().parent.parent

# Scopes required for Google Drive + Google Docs creation.
# Drive readonly is insufficient for the script doc flow because the
# repo creates and updates documents in Google Drive.
SCOPES = [
    'https://www.googleapis.com/auth/drive',
    'https://www.googleapis.com/auth/documents',
]

def main():
    parser = argparse.ArgumentParser(description='Generate Google Drive token.json for PipelineGen')
    parser.add_argument('--credentials', default=str(ROOT / 'credentials.json'), help='Path to credentials.json')
    parser.add_argument('--token', default=str(ROOT / 'token.json'), help='Path to output token.json')
    parser.add_argument('--console', action='store_true', help='Use console auth instead of local server')
    parser.add_argument('--manual', action='store_true', help='Print auth URL and ask for the redirected URL with code')
    args = parser.parse_args()

    creds = None
    # The file token.json stores the user's access and refresh tokens
    if os.path.exists(args.token):
        try:
            creds = Credentials.from_authorized_user_file(args.token, SCOPES)
        except Exception as e:
            print(f"Error loading existing token: {e}")

    # If there are no (valid) credentials available, let the user log in.
    if not creds or not creds.valid:
        if creds and creds.expired and creds.refresh_token:
            print("Token expired, attempting refresh...")
            try:
                creds.refresh(Request())
            except Exception as e:
                print(f"Refresh failed: {e}")
                creds = None
        
        if not creds:
            if not os.path.exists(args.credentials):
                print(f"Error: {args.credentials} not found. Please download it from Google Cloud Console.")
                return

            print("Starting OAuth2 flow...")
            flow = InstalledAppFlow.from_client_secrets_file(args.credentials, SCOPES)
            if args.manual:
                flow.redirect_uri = "http://localhost"
                os.environ.setdefault("OAUTHLIB_RELAX_TOKEN_SCOPE", "1")
                auth_url, _ = flow.authorization_url(
                    prompt="consent",
                    include_granted_scopes="true",
                )
                print("\nOpen this URL in your browser:\n")
                print(auth_url)
                print("\nAfter approval, copy the FULL redirected URL from the browser address bar and paste it here: ", end="", flush=True)
                redirected_url = input().strip()
                try:
                    flow.fetch_token(authorization_response=redirected_url)
                    creds = flow.credentials
                except Exception as e:
                    creds = flow.credentials
                    if not creds or not creds.token:
                        raise
                    print(f"Token exchange warning: {e}")
            elif args.console:
                flow.redirect_uri = "http://localhost"
                os.environ.setdefault("OAUTHLIB_RELAX_TOKEN_SCOPE", "1")
                auth_url, _ = flow.authorization_url(
                    prompt="consent",
                    include_granted_scopes="true",
                )
                print("\nOpen this URL in your browser:\n")
                print(auth_url)
                print("\nPaste the authorization code here: ", end="", flush=True)
                code = input().strip()
                try:
                    flow.fetch_token(code=code)
                    creds = flow.credentials
                except Exception as e:
                    # Some Google accounts return a token response with
                    # a superset of scopes; oauthlib may surface that as a
                    # warning/error even though the access token is usable.
                    # Retry by forcing the underlying credentials object if
                    # token exchange actually succeeded.
                    creds = flow.credentials
                    if not creds or not creds.token:
                        raise
                    print(f"Token exchange warning: {e}")
            else:
                creds = flow.run_local_server(port=0)

        # Save the credentials for the next run in the format expected by the Go app
        # Go app expects: access_token, token_type, refresh_token, expiry
        token_data = {
            "access_token": creds.token,
            "token_type": "Bearer", # Default for Google OAuth2
            "refresh_token": creds.refresh_token,
            "expiry": creds.expiry.isoformat() + "Z" if creds.expiry else None
        }

        with open(args.token, 'w') as token_file:
            json.dump(token_data, token_file, indent=2)
        
        print(f"Token successfully saved to {args.token}")

if __name__ == '__main__':
    main()
