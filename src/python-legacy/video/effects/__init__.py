"""
Placeholder package for optional video text effects.

Some workers previously relied on external "effects" folders downloaded separately.
Keeping this package ensures `modules/video/effects/` exists in deployments, so any
path-based discovery logic can succeed even when external assets are missing.
"""


