import subprocess
import sys
import logging

# Configure logging
logger = logging.getLogger(__name__)

# List of required packages
REQUIRED_PACKAGES = [
    "moviepy",
    "requests",
    "pysrt",
    "deep_translator",
    "argostranslate",
    "faster_whisper",
    "pydub",
    "torch",
    "gradio",
    "selenium",
    "undetected-chromedriver"
]

def check_and_install_dependencies(force_reinstall=False):
    """
    Checks for required packages and installs them if missing.
    Returns True if all dependencies are met, False otherwise.
    """
    missing_packages = []
    for package in REQUIRED_PACKAGES:
        try:
            __import__(package)
        except ImportError:
            missing_packages.append(package)

    if not missing_packages and not force_reinstall:
        logger.info("✅ All dependencies are already installed.")
        return True

    if force_reinstall:
        logger.info("Force reinstalling all dependencies...")
        packages_to_install = REQUIRED_PACKAGES
    else:
        logger.info(f"Missing packages: {', '.join(missing_packages)}")
        packages_to_install = missing_packages

    for package in packages_to_install:
        try:
            logger.info(f"Installing {package}...")
            command = [sys.executable, "-m", "pip", "install"]
            if force_reinstall:
                command.append("--force-reinstall")
            command.append(package)
            
            subprocess.check_call(command)
            logger.info(f"✅ Successfully installed {package}")
        except subprocess.CalledProcessError as e:
            logger.error(f"❌ Failed to install {package}: {e}")
            return False
    
    return True
