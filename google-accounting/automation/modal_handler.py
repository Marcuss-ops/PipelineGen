import logging

log = logging.getLogger("ModalHandler")

async def dismiss_vids_modals(page):
    """
    Safely dismisses common blocking popups in Google Vids by clicking their close buttons
    instead of aggressively deleting structural DOM elements, which breaks React event listeners.
    """
    try:
        # 1. Close "Getting Started" or similar welcome dialogs using their close button
        close_buttons = [
            'button[aria-label*="Close" i]',
            'button[aria-label*="Chiudi" i]',
            'div[role="button"][aria-label*="Close" i]',
            'div[role="button"][aria-label*="Chiudi" i]',
            '[data-view-id*="getting-started"] button',
            '.prerendered-getting-started-dialog-close-button',
        ]
        for selector in close_buttons:
            btn = page.locator(selector).first
            if await btn.count() > 0 and await btn.is_visible():
                log.info(f"Clicking dialog close button: {selector}")
                await btn.click()
                
    except Exception as e:
        log.warning(f"Error in dismiss_vids_modals: {e}")

async def start_modal_killer(page, stop_event):
    """Background task is kept for API compatibility, but runs safely and quietly."""
    # We do nothing here to prevent background loops from interfering with user interaction
    pass
