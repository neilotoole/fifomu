window.BENCHMARK_DATA = {
  "lastUpdate": 1779570592854,
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
      }
    ]
  }
}