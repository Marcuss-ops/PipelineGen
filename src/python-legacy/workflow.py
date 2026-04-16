"""
Compat shim (legacy import path).

Some scripts still import `workflow` from the root of `refactored/`.
The actual code is in the local workflow module.
"""

# Import from the current directory's workflow module
from .workflow import *  # noqa: F403

