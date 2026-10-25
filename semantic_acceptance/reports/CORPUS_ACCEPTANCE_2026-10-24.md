# Corpus acceptance — 40 downloaded transcripts

Worker: `../BrainRust/target/release/semantic-engine` · 220-word scene target · 40 scripts, 244040 words, 1075 scenes.

## Verdict

| check | value |
|---|---|
| hard failures | **37** |
| scripts with review notes | 3 |
| entity mentions | 1384 |
| grounded facts | 0 |
| dated facts | 0 |
| semantic groups | 1 |
| visual proposals | 2673 |
| visual queries (raw) | 5330 |
| consolidation candidates | 231 |
| consolidation search jobs | 139 |
| consolidation queries | 139 |
| canonical facts covered | 0/0 |
| years recovered | 1/1 |
| percentages recovered | 0/0 |
| wall clock | 0.2s |

A hard failure means a contract or grounding defect (ungrounded span, dangling id, inconsistent coverage). Review notes are observations (no fact, no group, a year the engine did not normalize); semantic relevance is not scored without independent annotation.

## Scripts

| script | lang | words | scenes | entities | facts | groups | prod | jobs | years | % | verdict |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| `-1aqvX0S9QM` | en | 1451 | 7 | 242 | 0 | 0 | 45 | 27 | 0/0 | 0/0 | REVIEW |
| `-HF-KbFABH8` | en | 1744 | 7 | 367 | 0 | 1 | 44 | 26 | 1/1 | 0/0 | REVIEW |
| `0nbjHpi8T_Y` | en | 4341 | 19 | 775 | 0 | 0 | 142 | 86 | 0/0 | 0/0 | REVIEW |
| `2SyyNHhfMM4` | - | - | - | - | - | - | - | - | FAIL |
| `2n0ypZhZ7cU` | - | - | - | - | - | - | - | - | FAIL |
| `2z53Ft0STog` | - | - | - | - | - | - | - | - | FAIL |
| `3VkyADt4XZk` | - | - | - | - | - | - | - | - | FAIL |
| `4V9MFTUEEfM` | - | - | - | - | - | - | - | - | FAIL |
| `5IzV-etVjfI` | - | - | - | - | - | - | - | - | FAIL |
| `6muiJVxMzwU` | - | - | - | - | - | - | - | - | FAIL |
| `6zWqzgtUhkU` | - | - | - | - | - | - | - | - | FAIL |
| `7INHO_tssOw` | - | - | - | - | - | - | - | - | FAIL |
| `8uhwxmPEeoA` | - | - | - | - | - | - | - | - | FAIL |
| `90Xbnr-kzhA` | - | - | - | - | - | - | - | - | FAIL |
| `9JSmR1Yre-Y` | - | - | - | - | - | - | - | - | FAIL |
| `9PYzlRu6zUQ` | - | - | - | - | - | - | - | - | FAIL |
| `9Q6T-bzF4Vs` | - | - | - | - | - | - | - | - | FAIL |
| `9wj70m0yVgk` | - | - | - | - | - | - | - | - | FAIL |
| `AeVxTyMX5cE` | - | - | - | - | - | - | - | - | FAIL |
| `AqAiKn9liIs` | - | - | - | - | - | - | - | - | FAIL |
| `B-0e8Dgi7C0` | - | - | - | - | - | - | - | - | FAIL |
| `BCmsGgZHZKg` | - | - | - | - | - | - | - | - | FAIL |
| `BDWuml8OX4c` | - | - | - | - | - | - | - | - | FAIL |
| `BE9DDOPzHLA` | - | - | - | - | - | - | - | - | FAIL |
| `CY4buLCQNVU` | - | - | - | - | - | - | - | - | FAIL |
| `Ce_yvMlgYI4` | - | - | - | - | - | - | - | - | FAIL |
| `Ert3HL-mggI` | - | - | - | - | - | - | - | - | FAIL |
| `FdrZm7NRqzg` | - | - | - | - | - | - | - | - | FAIL |
| `GFEueJYiqvU` | - | - | - | - | - | - | - | - | FAIL |
| `GKz7S2eCUD0` | - | - | - | - | - | - | - | - | FAIL |
| `GQCO94nH5ZQ` | - | - | - | - | - | - | - | - | FAIL |
| `Gh1Gzek4Vlw` | - | - | - | - | - | - | - | - | FAIL |
| `GzcIPJ8tmsI` | - | - | - | - | - | - | - | - | FAIL |
| `H2cJWeacoM0` | - | - | - | - | - | - | - | - | FAIL |
| `HT99qGWFpgM` | - | - | - | - | - | - | - | - | FAIL |
| `IA-01pLPa3w` | - | - | - | - | - | - | - | - | FAIL |
| `IOTyhPZbvU4` | - | - | - | - | - | - | - | - | FAIL |
| `IjmKwEJXIHk` | - | - | - | - | - | - | - | - | FAIL |
| `IuAEZiKopoU` | - | - | - | - | - | - | - | - | FAIL |
| `Ix06B4fI19M` | - | - | - | - | - | - | - | - | FAIL |

## Hard failures

- `2SyyNHhfMM4`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `2n0ypZhZ7cU`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `2z53Ft0STog`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `3VkyADt4XZk`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `4V9MFTUEEfM`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `5IzV-etVjfI`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `6muiJVxMzwU`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `6zWqzgtUhkU`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `7INHO_tssOw`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `8uhwxmPEeoA`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `90Xbnr-kzhA`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `9JSmR1Yre-Y`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `9PYzlRu6zUQ`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `9Q6T-bzF4Vs`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `9wj70m0yVgk`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `AeVxTyMX5cE`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `AqAiKn9liIs`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `B-0e8Dgi7C0`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `BCmsGgZHZKg`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `BDWuml8OX4c`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `BE9DDOPzHLA`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `CY4buLCQNVU`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `Ce_yvMlgYI4`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `Ert3HL-mggI`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `FdrZm7NRqzg`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `GFEueJYiqvU`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `GKz7S2eCUD0`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `GQCO94nH5ZQ`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `Gh1Gzek4Vlw`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `GzcIPJ8tmsI`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `H2cJWeacoM0`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `HT99qGWFpgM`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `IA-01pLPa3w`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `IOTyhPZbvU4`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `IjmKwEJXIHk`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `IuAEZiKopoU`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'
- `Ix06B4fI19M`: worker: [Errno 2] No such file or directory: '../BrainRust/target/release/semantic-engine'

## Review notes

- `-1aqvX0S9QM`: no grounded fact; no semantic group
- `-HF-KbFABH8`: no grounded fact
- `0nbjHpi8T_Y`: no grounded fact; no semantic group
