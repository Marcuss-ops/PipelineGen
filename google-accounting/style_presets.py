"""Canonical style presets shared across prompt generation, local folders, and Drive seeding."""

from collections import OrderedDict


STYLE_PRESETS = OrderedDict(
    {
        "realistic": "extremely detailed, realistic, 8k, photorealistic, cinematic lighting",
        "cartoon": "cartoon style, 2d, colorful, high quality animation, vibrant",
        "medieval": "medieval style, fantasy, historical, detailed oil painting, epic atmosphere",
        "cyberpunk": "cyberpunk aesthetic, neon lights, futuristic, dark atmosphere, high tech",
        "watercolor": "watercolor painting style, soft colors, artistic, fluid textures",
        "3d-render": "3d render, octane render, unreal engine 5 style, volumetric lighting, masterpiece",
        "sketch": "hand-drawn sketch, pencil drawing, monochrome, detailed lines, artistic",
        "cinematic": "cinematic lighting, movie shot, 35mm lens, highly detailed, dramatic",
        "whiteboard": "clean whiteboard animation frame, bold black marker strokes on pure white background, infographic style, simple icon drawings, numbered steps layout, arrows connecting concepts, geometric shapes, no text or words, flat 2d illustration, high contrast black on white, educational diagram aesthetic, single concept per frame",
        "kawaii": "cute kawaii style, chibi characters, pastel colors, soft lighting, 2d vector art, minimalist, adorable",
        "anime": "modern anime style, cinematic lighting, vibrant colors, cel-shaded, highly detailed 2d digital art",
        "retro-print": "retro print illustration, vintage poster style, 1970s aesthetic, risograph texture, muted warm colors, bold graphic design",
        "heritage": "heritage style, 19th-century encyclopedia illustration, fine ink cross-hatching, vintage etching, muted natural colors, classical drawing",
        "papercraft": "papercraft style, layered paper art, 3d paper cutout, sharp drop shadows, textured cardstock, diorama aesthetic",
    }
)


STYLE_FOLDER_NAMES = tuple(STYLE_PRESETS.keys())

