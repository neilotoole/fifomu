window.BENCHMARK_DATA = {
  "lastUpdate": 1779570882530,
  "repoUrl": "https://github.com/neilotoole/fifomu",
  "entries": {
    "fifomu benchmarks": [
      {
        "commit": {
          "author": {
            "email": "neilotoole@apache.org",
            "name": "neilotoole",
            "username": "neilotoole"
          },
          "committer": {
            "email": "neilotoole@apache.org",
            "name": "neilotoole",
            "username": "neilotoole"
          },
          "distinct": true,
          "id": "b1965b42b43dbafe925a4ee21a09507e7ce86b03",
          "message": "ci: track benchmark performance and alert on regressions\n\nAdd a `bench` job that runs the benchmarks on the ubuntu leg and appends\nresults to the orphan `benchmark-data` branch via\nbenchmark-action/github-action-benchmark, building a performance trend\nover time (chart at dev/bench/ on that branch).\n\nRuns only on pushes to master, where the GITHUB_TOKEN is writable; the\njob grants itself contents: write while the rest of the workflow stays\nread-only. A regression past the 200% threshold posts a commit comment\n(cc @neilotoole) but does not fail the build, since shared CI runners are\ntoo noisy for a hard perf gate.",
          "timestamp": "2026-05-23T15:08:58-06:00",
          "tree_id": "25e5eb65459777f0f6ac558e5400f825d67d906a",
          "url": "https://github.com/neilotoole/fifomu/commit/b1965b42b43dbafe925a4ee21a09507e7ce86b03"
        },
        "date": 1779570591700,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkLockContext_FastPath",
            "value": 15.84,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "69789457 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_FastPath - ns/op",
            "value": 15.84,
            "unit": "ns/op",
            "extra": "69789457 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_FastPath - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "69789457 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_FastPath - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "69789457 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Contended",
            "value": 318.7,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "3421452 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Contended - ns/op",
            "value": 318.7,
            "unit": "ns/op",
            "extra": "3421452 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Contended - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "3421452 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Contended - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "3421452 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Cancel",
            "value": 143.8,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "8382662 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Cancel - ns/op",
            "value": 143.8,
            "unit": "ns/op",
            "extra": "8382662 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Cancel - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "8382662 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Cancel - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "8382662 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/stdlib",
            "value": 6.011,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "199405125 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/stdlib - ns/op",
            "value": 6.011,
            "unit": "ns/op",
            "extra": "199405125 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "199405125 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "199405125 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/fifomu",
            "value": 13.28,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "89738632 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/fifomu - ns/op",
            "value": 13.28,
            "unit": "ns/op",
            "extra": "89738632 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "89738632 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "89738632 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/semaphoreMu",
            "value": 14.77,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "83446263 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/semaphoreMu - ns/op",
            "value": 14.77,
            "unit": "ns/op",
            "extra": "83446263 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/semaphoreMu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "83446263 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/semaphoreMu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "83446263 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/stdlib",
            "value": 53.25,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "22764212 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/stdlib - ns/op",
            "value": 53.25,
            "unit": "ns/op",
            "extra": "22764212 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "22764212 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "22764212 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/fifomu",
            "value": 245.2,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "4945532 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/fifomu - ns/op",
            "value": 245.2,
            "unit": "ns/op",
            "extra": "4945532 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "4945532 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "4945532 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/semaphoreMu",
            "value": 452.2,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "2711962 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/semaphoreMu - ns/op",
            "value": 452.2,
            "unit": "ns/op",
            "extra": "2711962 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "2711962 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "2711962 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/stdlib",
            "value": 116.1,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "10297264 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/stdlib - ns/op",
            "value": 116.1,
            "unit": "ns/op",
            "extra": "10297264 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "10297264 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "10297264 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/fifomu",
            "value": 248,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "4936894 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/fifomu - ns/op",
            "value": 248,
            "unit": "ns/op",
            "extra": "4936894 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "4936894 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "4936894 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/semaphoreMu",
            "value": 439.3,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "2750188 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/semaphoreMu - ns/op",
            "value": 439.3,
            "unit": "ns/op",
            "extra": "2750188 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "2750188 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "2750188 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/stdlib",
            "value": 68.86,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "17579598 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/stdlib - ns/op",
            "value": 68.86,
            "unit": "ns/op",
            "extra": "17579598 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "17579598 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "17579598 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/fifomu",
            "value": 277.8,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "4350532 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/fifomu - ns/op",
            "value": 277.8,
            "unit": "ns/op",
            "extra": "4350532 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "4350532 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "4350532 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/semaphoreMu",
            "value": 476.3,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "2524522 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/semaphoreMu - ns/op",
            "value": 476.3,
            "unit": "ns/op",
            "extra": "2524522 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "2524522 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "2524522 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/stdlib",
            "value": 129.6,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "9685285 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/stdlib - ns/op",
            "value": 129.6,
            "unit": "ns/op",
            "extra": "9685285 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "9685285 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "9685285 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/fifomu",
            "value": 274,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "4385520 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/fifomu - ns/op",
            "value": 274,
            "unit": "ns/op",
            "extra": "4385520 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "4385520 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "4385520 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/semaphoreMu",
            "value": 457.9,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "2624834 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/semaphoreMu - ns/op",
            "value": 457.9,
            "unit": "ns/op",
            "extra": "2624834 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "2624834 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "2624834 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/stdlib",
            "value": 391,
            "unit": "ns/op\t      12 B/op\t       0 allocs/op",
            "extra": "3079870 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/stdlib - ns/op",
            "value": 391,
            "unit": "ns/op",
            "extra": "3079870 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/stdlib - B/op",
            "value": 12,
            "unit": "B/op",
            "extra": "3079870 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "3079870 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/fifomu",
            "value": 971.6,
            "unit": "ns/op\t      12 B/op\t       0 allocs/op",
            "extra": "1228933 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/fifomu - ns/op",
            "value": 971.6,
            "unit": "ns/op",
            "extra": "1228933 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/fifomu - B/op",
            "value": 12,
            "unit": "B/op",
            "extra": "1228933 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "1228933 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/semaphoreMu",
            "value": 1085,
            "unit": "ns/op\t      56 B/op\t       1 allocs/op",
            "extra": "1000000 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/semaphoreMu - ns/op",
            "value": 1085,
            "unit": "ns/op",
            "extra": "1000000 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/semaphoreMu - B/op",
            "value": 56,
            "unit": "B/op",
            "extra": "1000000 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/semaphoreMu - allocs/op",
            "value": 1,
            "unit": "allocs/op",
            "extra": "1000000 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/stdlib",
            "value": 1199,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "994159 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/stdlib - ns/op",
            "value": 1199,
            "unit": "ns/op",
            "extra": "994159 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "994159 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "994159 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/fifomu",
            "value": 2610,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "452409 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/fifomu - ns/op",
            "value": 2610,
            "unit": "ns/op",
            "extra": "452409 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "452409 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "452409 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/semaphoreMu",
            "value": 3108,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "377276 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/semaphoreMu - ns/op",
            "value": 3108,
            "unit": "ns/op",
            "extra": "377276 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "377276 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "377276 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "neilotoole@apache.org",
            "name": "neilotoole",
            "username": "neilotoole"
          },
          "committer": {
            "email": "neilotoole@apache.org",
            "name": "neilotoole",
            "username": "neilotoole"
          },
          "distinct": true,
          "id": "2faa390f1303f9578297354a726e4722391a21f8",
          "message": "ci: add PR-time benchmark comparison against master baseline\n\nAdd a `bench-pr` job that benchmarks the PR head and compares it against\nthe latest entry on benchmark-data, rendering the diff in the run summary\nand commenting only on a >=200% regression. It never pushes the PR's own\nnumbers to history and never fails the build.\n\nGated to same-repo, non-Dependabot PRs: those are the only ones whose\npull_request GITHUB_TOKEN can read the repo and post comments. The job\ngrants itself pull-requests: write while keeping contents read-only.",
          "timestamp": "2026-05-23T15:13:44-06:00",
          "tree_id": "ad1e3f40079a7719e840ab010dbb1c7a12898a3e",
          "url": "https://github.com/neilotoole/fifomu/commit/2faa390f1303f9578297354a726e4722391a21f8"
        },
        "date": 1779570881789,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkLockContext_FastPath",
            "value": 5.843,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "209761231 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_FastPath - ns/op",
            "value": 5.843,
            "unit": "ns/op",
            "extra": "209761231 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_FastPath - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "209761231 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_FastPath - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "209761231 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Contended",
            "value": 246.4,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "4864498 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Contended - ns/op",
            "value": 246.4,
            "unit": "ns/op",
            "extra": "4864498 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Contended - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "4864498 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Contended - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "4864498 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Cancel",
            "value": 128.1,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "9312798 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Cancel - ns/op",
            "value": 128.1,
            "unit": "ns/op",
            "extra": "9312798 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Cancel - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "9312798 times\n4 procs"
          },
          {
            "name": "BenchmarkLockContext_Cancel - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "9312798 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/stdlib",
            "value": 2.295,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "521727439 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/stdlib - ns/op",
            "value": 2.295,
            "unit": "ns/op",
            "extra": "521727439 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "521727439 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "521727439 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/fifomu",
            "value": 5.477,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "218912520 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/fifomu - ns/op",
            "value": 5.477,
            "unit": "ns/op",
            "extra": "218912520 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "218912520 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "218912520 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/semaphoreMu",
            "value": 7.423,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "161742602 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/semaphoreMu - ns/op",
            "value": 7.423,
            "unit": "ns/op",
            "extra": "161742602 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/semaphoreMu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "161742602 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexUncontended/semaphoreMu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "161742602 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/stdlib",
            "value": 14.84,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "80286724 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/stdlib - ns/op",
            "value": 14.84,
            "unit": "ns/op",
            "extra": "80286724 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "80286724 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "80286724 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/fifomu",
            "value": 204.9,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "5880604 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/fifomu - ns/op",
            "value": 204.9,
            "unit": "ns/op",
            "extra": "5880604 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "5880604 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "5880604 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/semaphoreMu",
            "value": 353.8,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "3405007 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/semaphoreMu - ns/op",
            "value": 353.8,
            "unit": "ns/op",
            "extra": "3405007 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "3405007 times\n4 procs"
          },
          {
            "name": "BenchmarkMutex/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "3405007 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/stdlib",
            "value": 58.3,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "20768413 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/stdlib - ns/op",
            "value": 58.3,
            "unit": "ns/op",
            "extra": "20768413 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "20768413 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "20768413 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/fifomu",
            "value": 208.1,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "5644599 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/fifomu - ns/op",
            "value": 208.1,
            "unit": "ns/op",
            "extra": "5644599 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "5644599 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "5644599 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/semaphoreMu",
            "value": 351.3,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "3403111 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/semaphoreMu - ns/op",
            "value": 351.3,
            "unit": "ns/op",
            "extra": "3403111 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "3403111 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSlack/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "3403111 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/stdlib",
            "value": 46.11,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "25867033 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/stdlib - ns/op",
            "value": 46.11,
            "unit": "ns/op",
            "extra": "25867033 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "25867033 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "25867033 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/fifomu",
            "value": 245.6,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "4870255 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/fifomu - ns/op",
            "value": 245.6,
            "unit": "ns/op",
            "extra": "4870255 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "4870255 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "4870255 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/semaphoreMu",
            "value": 404,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "2986863 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/semaphoreMu - ns/op",
            "value": 404,
            "unit": "ns/op",
            "extra": "2986863 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "2986863 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWork/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "2986863 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/stdlib",
            "value": 51.26,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "23618206 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/stdlib - ns/op",
            "value": 51.26,
            "unit": "ns/op",
            "extra": "23618206 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "23618206 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "23618206 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/fifomu",
            "value": 263,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "4809061 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/fifomu - ns/op",
            "value": 263,
            "unit": "ns/op",
            "extra": "4809061 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "4809061 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "4809061 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/semaphoreMu",
            "value": 381,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "2789654 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/semaphoreMu - ns/op",
            "value": 381,
            "unit": "ns/op",
            "extra": "2789654 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "2789654 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexWorkSlack/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "2789654 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/stdlib",
            "value": 279.9,
            "unit": "ns/op\t      12 B/op\t       0 allocs/op",
            "extra": "4305398 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/stdlib - ns/op",
            "value": 279.9,
            "unit": "ns/op",
            "extra": "4305398 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/stdlib - B/op",
            "value": 12,
            "unit": "B/op",
            "extra": "4305398 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "4305398 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/fifomu",
            "value": 800.2,
            "unit": "ns/op\t      12 B/op\t       0 allocs/op",
            "extra": "1518543 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/fifomu - ns/op",
            "value": 800.2,
            "unit": "ns/op",
            "extra": "1518543 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/fifomu - B/op",
            "value": 12,
            "unit": "B/op",
            "extra": "1518543 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "1518543 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/semaphoreMu",
            "value": 902.7,
            "unit": "ns/op\t      55 B/op\t       1 allocs/op",
            "extra": "1319175 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/semaphoreMu - ns/op",
            "value": 902.7,
            "unit": "ns/op",
            "extra": "1319175 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/semaphoreMu - B/op",
            "value": 55,
            "unit": "B/op",
            "extra": "1319175 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexNoSpin/semaphoreMu - allocs/op",
            "value": 1,
            "unit": "allocs/op",
            "extra": "1319175 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/stdlib",
            "value": 745.7,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "1596726 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/stdlib - ns/op",
            "value": 745.7,
            "unit": "ns/op",
            "extra": "1596726 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/stdlib - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "1596726 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/stdlib - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "1596726 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/fifomu",
            "value": 2231,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "532144 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/fifomu - ns/op",
            "value": 2231,
            "unit": "ns/op",
            "extra": "532144 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/fifomu - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "532144 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/fifomu - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "532144 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/semaphoreMu",
            "value": 2678,
            "unit": "ns/op\t     175 B/op\t       2 allocs/op",
            "extra": "427302 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/semaphoreMu - ns/op",
            "value": 2678,
            "unit": "ns/op",
            "extra": "427302 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/semaphoreMu - B/op",
            "value": 175,
            "unit": "B/op",
            "extra": "427302 times\n4 procs"
          },
          {
            "name": "BenchmarkMutexSpin/semaphoreMu - allocs/op",
            "value": 2,
            "unit": "allocs/op",
            "extra": "427302 times\n4 procs"
          }
        ]
      }
    ]
  }
}