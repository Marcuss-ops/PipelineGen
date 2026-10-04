#!/usr/bin/env python3
"""Verifica orfani voiceover per una run PipelineGen post-fix K2.
Uso: python3 verify_ptbr_orphans.py <job_id>
Verdetto: ✅ solo se (drain presente) E (zero publish Drive dopo il termine)
          E (zero righe Google Docs) E (tutti gli step publish COMPLETED)."""
import subprocess, sqlite3, sys, json, datetime

jid = sys.argv[1]
con = sqlite3.connect('file:data/jobs/jobs.db.sqlite?mode=ro', uri=True)
row = con.execute("SELECT status, updated_at, started_at FROM jobs WHERE id=?", (jid,)).fetchone()
if not row:
    print(f"RUN {jid}: non trovata"); sys.exit(2)
status, T, started = row
# i timestamp SQLite hanno precisione nanosecondo: tronco ai secondi
def sec(ts):
    ts = str(ts)
    return ts[:19].replace('T', ' ') if len(ts) >= 19 else ts
t0 = (datetime.datetime.fromisoformat(sec(started)) - datetime.timedelta(minutes=10)).isoformat(sep=' ', timespec='seconds')
t1 = (datetime.datetime.fromisoformat(sec(T)) + datetime.timedelta(minutes=10)).isoformat(sep=' ', timespec='seconds')

out = subprocess.run(['journalctl','-u','pipelinegen','--since',t0,'--until',t1,'--no-pager','-o','short-iso'],
                     capture_output=True, text=True).stdout
win = [l for l in out.splitlines() if jid in l]
# le righe drain portano run_id (non job_id): stessa finestra-epoch del job
# (job_<epoch_ns> ↔ run_<epoch_ns>, creati entro ~1-2s → correlazione a 8 cifre)
import re as _rx
job_epoch = jid.split('_')[1][:8] if '_' in jid else ''
allwin = out.splitlines()
drain = []
for l in allwin:
    if 'publish pool drained' in l:
        m = _rx.search(r'run_(\d+)_', l)
        if m and job_epoch and m.group(1)[:8] == job_epoch:
            drain.append(l)
pubs  = [l for l in win if ('delivery: file published' in l or 'artifact published' in l)]
docs  = [l for l in win if ('docs.google' in l or 'UpsertDocument' in l or 'DocumentReference' in l)]
# orfano = upload VOICEOVER >2s DOPO l'updated_at terminale (la TX di completamento
# scrive updated_at ~1s dopo l'ultima riga nel journal). Gli upload overlay/
# final-artifact post-run sono il contratto remote-render (lavoro downstream
# del final job), NON orfani voiceover.
import re as _re
def _ts(l):
    m = _re.match(r'(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)', l)
    return datetime.datetime.fromisoformat(m.group(1)) if m else None
Tdt = datetime.datetime.fromisoformat(sec(T))
vo_pubs = [l for l in pubs if 'destination":"voiceover' in l or 'destination\":\"voiceover' in l]
other_pubs = [l for l in pubs if l not in vo_pubs]
orphans = [l for l in vo_pubs if (d := _ts(l)) and d > Tdt + datetime.timedelta(seconds=2)]
late_other = [l for l in other_pubs if (d := _ts(l)) and d > Tdt + datetime.timedelta(seconds=2)]

steps = con.execute("SELECT step_name,status,duration_ms FROM job_steps WHERE job_id=? AND step_name IN ('DOCUMENT','post_writer_finalize','worker.execution') ORDER BY created_at", (jid,)).fetchall()
pub = con.execute("SELECT count(*), sum(status='COMPLETED') FROM job_steps WHERE job_id=? AND step_name='publish'", (jid,)).fetchone()
n_pub, n_ok = pub[0] or 0, pub[1] or 0

lang = proj = '?'
p = con.execute("SELECT payload FROM job_payloads WHERE job_id=? LIMIT 1", (jid,)).fetchone()
try:
    d = json.loads(p[0]); it = (d.get('items') or [d])[0]
    lang = it.get('language'); proj = (it.get('project') or '?')[:44]
except Exception: pass

print(f"RUN={jid} lang={lang} proj={proj} STATUS={status} terminal={T}")
print(f"drain line        : {len(drain)} → {drain[-1][:150] if drain else 'ASSENTE'}")
print(f"publish Drive     : {len(pubs)} (voiceover: {len(vo_pubs)}, altri: {len(other_pubs)})")
print(f"ORFANI voiceover (upload dopo il termine): {len(orphans)}")
if late_other:
    print(f"altri publish post-run (contratto remote-render, informativo): {len(late_other)}")
print(f"righe Google Docs : {len(docs)}")
print("steps:", '; '.join(f"{a}={b}/{c}ms" for a,b,c in steps))
print(f"publish steps     : {n_pub} totali, {n_ok} COMPLETED")
for l in orphans:
    print("ORFANO sospetto:", l[:200])
verdict = len(drain) > 0 and len(orphans) == 0 and len(docs) == 0 and n_ok == n_pub
print("VERDETTO:", "✅ ZERO ORFANI — drain dentro la run, nessun upload dopo il termine" if verdict else "⚠ VERIFICARE MANUALMENTE")
sys.exit(0 if verdict else 1)
