#!/usr/bin/env python3
import os
import json
import argparse
from google_auth_oauthlib.flow import InstalledAppFlow
from google.auth.transport.requests import Request
from google.oauth2.credentials import Credentials

# Scopes required for Google Drive access
SCOPES = ['https://www.googleapis.com/auth/drive.file', 'https://www.googleapis.com/auth/drive.metadata.readonly']

def main():
    parser = argparse.ArgumentParser(description='Generate Google Drive token.json for PipelineGen')
    parser.add_argument('--credentials', default='credentials.json', help='Path to credentials.json')
    parser.add_argument('--token', default='token.json', help='Path to output token.json')
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
