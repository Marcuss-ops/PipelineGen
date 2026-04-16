#!/usr/bin/env python3
import argparse
import os
import subprocess
import sys
import shutil
from pathlib import Path
from typing import Optional


def _find_base_dir(script_path: Path) -> Path:
    # Supported layouts:
    # - /opt/VeloxEditing/current/refactored/worker_bootstrap.py
    # - /opt/VeloxEditing/releases/<ts>/refactored/worker_bootstrap.py
    for parent in script_path.parents:
        if parent.name == "current":
            return parent.parent
        if parent.name == "releases":
            return parent.parent
    # Fallback: assume .../<base>/(current|releases)/<something>/refactored/script.py
    return script_path.parents[2]


def _pick_python_in_venv(venv_dir: Path) -> Optional[Path]:
    for candidate in (
        venv_dir / "bin" / "python3",
        venv_dir / "bin" / "python",
    ):
        if candidate.exists() and os.access(candidate, os.X_OK):
            return candidate
    return None


def _ensure_pip(python_in_venv: Path) -> None:
    try:
        probe = subprocess.run(
            [str(python_in_venv), "-m", "pip", "--version"],
            capture_output=True,
            text=True,
            check=False,
        )
        if probe.returncode == 0:
            return
    except Exception:
        pass

    print(f"📦 Verifico/aggiorno pip in {python_in_venv}...")
    subprocess.run([str(python_in_venv), "-m", "ensurepip", "--upgrade"], check=False, capture_output=False)


def _ensure_min_deps(python_in_venv: Path) -> None:
    # Il worker deve poter importare i moduli di base prima di avviarsi.
    # In particolare: `requests` (heartbeat) e `gradio` (standalone_multi_video importa gradio).
    required = ["requests", "gradio"]
    missing: list[str] = []
    for pkg in required:
        try:
            probe = subprocess.run(
                [str(python_in_venv), "-c", f"import {pkg}"],
                capture_output=True,
                text=True,
                check=False,
            )
            if probe.returncode != 0:
                missing.append(pkg)
        except Exception:
            missing.append(pkg)

    if not missing:
        return

    print(f"📦 Installazione dipendenze minime: {', '.join(missing)}...")
    subprocess.run([str(python_in_venv), "-m", "pip", "install", *missing], check=False, capture_output=False)


def _ensure_venv(venv_dir: Path, requirements_path: Path) -> Path:
    python_in_venv = _pick_python_in_venv(venv_dir)
    if python_in_venv is None:
        venv_dir.parent.mkdir(parents=True, exist_ok=True)
        # Rimuovi venv esistente se corrotto
        if venv_dir.exists():
            try:
                shutil.rmtree(venv_dir)
                print(f"🗑️ Venv esistente rimosso: {venv_dir}")
            except Exception as e:
                print(f"⚠️ Impossibile rimuovere venv esistente: {e}")

        print(f"🔨 Creazione venv in {venv_dir}...")
        result = subprocess.run(
            ["/usr/bin/python3", "-m", "venv", str(venv_dir)],
            check=False,
            capture_output=False,
            # text=True,  # Non serve con capture_output=False
            timeout=300,  # Timeout aumentato a 5 min
        )
        if result.returncode != 0:
            # Prova con python3.12, python3.11, ecc. se python3 fallisce
            for py_cmd in ["python3.12", "python3.11", "python3.10", "python3.9"]:
                if shutil.which(py_cmd):
                    print(f"🔄 Tentativo con {py_cmd}...")
                    print(f"🔄 Tentativo creazione venv con {py_cmd}...")
                    result = subprocess.run(
                        [py_cmd, "-m", "venv", str(venv_dir)],
                        check=False,
                        capture_output=False,
                        # text=True,
                        timeout=300,
                    )
                    if result.returncode == 0:
                        print(f"✅ Venv creato con {py_cmd}")
                        break

        if result.returncode != 0:
            error_msg = result.stderr or result.stdout or "Errore sconosciuto"
            raise RuntimeError(
                f"Impossibile creare venv in {venv_dir}.\n"
                f"Errore: {error_msg}\n"
                f"💡 Verifica che python3-venv sia installato: sudo apt install python3-venv"
            )

        python_in_venv = _pick_python_in_venv(venv_dir)
        if python_in_venv is None:
            raise RuntimeError(f"venv creato ma python non trovato in {venv_dir}")

    _ensure_pip(python_in_venv)
    # Prova ad aggiornare pip, ma non fallire se non funziona
    print(f"📦 Aggiornamento pip in {venv_dir}...")
    pip_result = subprocess.run(
        [str(python_in_venv), "-m", "pip", "install", "--upgrade", "pip"],
        check=False,
        capture_output=False,
        # text=True,
        timeout=120,
    )
    if pip_result.returncode != 0:
        print(f"⚠️ Avviso: aggiornamento pip fallito (non critico): {pip_result.stderr}")

    # Evita di reinstallare ogni boot: usa marker file basato su mtime di requirements.txt
    marker = venv_dir / ".velox_deps_ok"
    should_install = True
    try:
        if requirements_path.exists() and marker.exists():
            should_install = marker.stat().st_mtime < requirements_path.stat().st_mtime
    except Exception:
        should_install = True

    if requirements_path.exists() and should_install:
        print(f"📦 Installazione requirements da {requirements_path} (questo potrebbe richiedere tempo)...")
        result = subprocess.run(
            [str(python_in_venv), "-m", "pip", "install", "-r", str(requirements_path)],
            capture_output=False, # Streaming output a video/log
            # text=True, 
            check=False,
        )
        if result.returncode == 0:
            try:
                marker.touch()
            except Exception:
                pass
        else:
            stderr = (result.stderr or result.stdout or "").strip()
            if stderr:
                stderr = stderr[-800:]
            print(
                f"[velox-worker bootstrap] pip install -r requirements.txt fallito (exit {result.returncode}). "
                "Non avvio il worker: verrà ritentato al prossimo avvio (systemd restart).\n"
                f"{stderr}"
            )
            raise RuntimeError("Dipendenze non installate: pip -r requirements.txt fallito")

    _ensure_min_deps(python_in_venv)
    return python_in_venv


def _report_boot_status(master_url: str, worker_name: Optional[str], status: str) -> None:
    """Invia un battito cardiaco minimo al master per mostrare lo stato nel dashboard durante il bootstrap."""
    if not master_url:
        return
    try:
        import requests
        from datetime import datetime
        payload = {
            "worker_id": worker_name or "bootstrap-worker",
            "worker_name": worker_name or "bootstrap-worker",
            "status": status,
            "timestamp": datetime.now().isoformat()
        }
        # Nota: l'endpoint potrebbe richiedere auth token, ma il bootstrap spesso non l'ha ancora.
        requests.post(f"{master_url}/heartbeat", json=payload, timeout=5)
    except Exception:
        pass


def main() -> int:
    parser = argparse.ArgumentParser(description="Bootstrap del worker Velox (self-healing venv)")
    parser.add_argument("--master-url", type=str, required=True)
    parser.add_argument("--worker-name", type=str, default=None)
    parser.add_argument("--poll-interval", type=int, default=5)
    
    # Usa parse_known_args per tollerare argomenti extra da systemd/ansible
    args, unknown = parser.parse_known_args()
    if unknown:
        print(f"⚠️ Ignorati argomenti bootstrap sconosciuti: {unknown}")

    _report_boot_status(args.master_url, args.worker_name, "bootstrapping")

    script_path = Path(__file__).resolve()
    base_dir = _find_base_dir(script_path)

    current_dir = base_dir / "current"
    if not current_dir.exists():
        # Best effort: se siamo dentro una release, usa quella come root codice.
        # (Questo permette avvio anche se il symlink current è assente.)
        current_dir = script_path.parents[1]

    # pip può crashare se os.getcwd() punta a una directory eliminata.
    # Forziamo una cwd stabile prima di creare/aggiornare il venv.
    try:
        os.chdir(str(current_dir if current_dir.exists() else base_dir))
    except Exception:
        try:
            os.chdir("/tmp")
        except Exception:
            pass

    requirements_path = script_path.parent / "requirements.txt"

    venv_dir_candidates = [
        current_dir / "venv",  # venv per-release (dentro current -> release)
        base_dir / "venv",  # venv persistente (fuori dalle release)
    ]
    venv_dir = next(
        (p for p in venv_dir_candidates if _pick_python_in_venv(p) is not None),
        venv_dir_candidates[0],
    )

    python_in_venv = _ensure_venv(venv_dir, requirements_path)

    # Assicura import "flat" (es. `import prompts`) anche se lo script entrypoint non è dentro refactored
    # o se WorkingDirectory viene cambiata da systemd.
    refactored_dir_candidates = [
        current_dir / "refactored",
        script_path.parent,
        base_dir / "refactored",
    ]
    refactored_dirs = [str(p) for p in refactored_dir_candidates if p.exists() and p.is_dir()]
    if refactored_dirs:
        existing = os.environ.get("PYTHONPATH", "")
        prefix = os.pathsep.join(refactored_dirs)
        os.environ["PYTHONPATH"] = prefix + (os.pathsep + existing if existing else "")

    worker_entrypoints: list[Path] = []
    # Layout più comune: releases/<ts>/refactored/job_worker.py (current -> releases/<ts>)
    worker_entrypoints.extend(
        [
            current_dir / "refactored" / "job_worker.py",
            current_dir / "job_worker.py",
            script_path.parent / "job_worker.py",
            base_dir / "refactored" / "job_worker.py",
            base_dir / "job_worker.py",
        ]
    )
    # Fallback: cerca nella release più recente
    releases_dir = base_dir / "releases"
    try:
        if releases_dir.exists() and releases_dir.is_dir():
            latest_release = sorted(releases_dir.iterdir(), key=lambda p: p.stat().st_mtime, reverse=True)[0]
            worker_entrypoints.extend(
                [
                    latest_release / "refactored" / "job_worker.py",
                    latest_release / "job_worker.py",
                ]
            )
    except Exception:
        pass

    worker_script = next((p for p in worker_entrypoints if p and p.exists()), None)
    if worker_script is None:
        raise FileNotFoundError(f"job_worker.py non trovato (cercati: {worker_entrypoints})")

    cmd = [
        str(python_in_venv),
        str(worker_script),
        "--master",
        args.master_url,
        # "--poll-interval", 
        # str(args.poll_interval),
    ]
    if args.worker_name:
        cmd.extend(["--name", args.worker_name])

    os.execv(cmd[0], cmd)
    return 0


if __name__ == "__main__":
    sys.exit(main())
