package main

import (
	"encoding/json"
	"fmt"
	"github.com/HouzuoGuo/tiedot/db"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Whether to clean up (delete benchmark DB) after benchmark
var benchCleanup = true

// Size of benchmark sample
var benchSize = 400000

// Run the benchmark function a number of times across multiple goroutines, and print out performance data.
func average(name string, fun func()) {
	numThreads := runtime.GOMAXPROCS(-1)
	wp := new(sync.WaitGroup)
	iter := float64(benchSize)
	start := float64(time.Now().UTC().UnixNano())
	// Run function across multiple goroutines
	for i := 0; i < benchSize; i += benchSize / numThreads {
		wp.Add(1)
		go func() {
			defer wp.Done()
			for j := 0; j < benchSize/numThreads; j++ {
				fun()
			}
		}()
	}
	wp.Wait()
	end := float64(time.Now().UTC().UnixNano())
	fmt.Printf("%s %d: %d ns/iter, %d iter/sec\n", name, int(benchSize), int((end-start)/iter), int(1000000000/((end-start)/iter)))
}

// Create a temporary database and collection for benchmark use.
func mkTmpDBAndCol(dbPath string, colName string) (col *db.Col) {
	os.RemoveAll(dbPath)
	tmpDB, err := db.OpenDB(dbPath)
	if err != nil {
		panic(err)
	}
	tmpCol, err := db.OpenCol(tmpDB, colName)
	if err != nil {
		panic(err)
	}
	return tmpCol
}

func sampleDoc() (js map[string]interface{}) {
	doc := fmt.Sprintf(`
{
	"nested": {
		"nested": {
			"str": "%s",
			"int": %d,
			"float": %f
		}
	},
	"strs": ["%s", "%s"],
	"ints": [%d, %d],
	"floats": [%f, %f]
}
`, strconv.FormatFloat(rand.Float64(), 'f', 6, 64), rand.Intn(benchSize), rand.Float64(),
		strconv.FormatFloat(rand.Float64(), 'f', 6, 64), strconv.FormatFloat(rand.Float64(), 'f', 6, 64),
		rand.Intn(benchSize), rand.Intn(benchSize),
		rand.Float32(), rand.Float32())
	if err := json.Unmarshal([]byte(doc), &js); err != nil {
		panic(err)
	}
	return
}

func sampleQuery() (js interface{}) {
	rangeStart := rand.Intn(benchSize)
	q := fmt.Sprintf(`
[
	{ "c": [
		{"eq": %d, "in": ["nested", "nested", "int"]},
		{"eq": %d, "in": ["ints"]}
	] },
	{ "int-from": %d, "int-to": %d, "in": ["ints"]}
]
`, rand.Intn(benchSize), rand.Intn(benchSize), rangeStart, rangeStart+2)
	if err := json.Unmarshal([]byte(q), &js); err != nil {
		panic(err)
	}
	return
}

// Document CRUD benchmark (insert/read/query/update/delete), intended for catching performance regressions.
func benchmark() {
	ids := make([]int, 0, benchSize)
	// Prepare a collection with two indexes
	tmp := "/tmp/tiedot_bench"
	if benchCleanup {
		defer os.RemoveAll(tmp)
	}
	col := mkTmpDBAndCol(tmp, "tmp")
	col.Index([]string{"nested", "nested", "str"})
	col.Index([]string{"nested", "nested", "int"})
	col.Index([]string{"nested", "nested", "float"})
	col.Index([]string{"strs"})
	col.Index([]string{"ints"})
	col.Index([]string{"floats"})

	// Benchmark document insert
	average("insert", func() {
		if _, err := col.Insert(sampleDoc()); err != nil {
			fmt.Println("Insert error", err)
		}
	})

	// Collect all document IDs and benchmark document read
	col.ForEachDoc(func(id int, _ []byte) bool {
		ids = append(ids, id)
		return true
	})
	average("read", func() {
		doc, err := col.Read(ids[rand.Intn(benchSize)])
		if doc == nil || err != nil {
			fmt.Println("Read error", doc, err)
		}
	})

	// Benchmark lookup query (two attributes)
	average("lookup", func() {
		result := make(map[int]struct{})
		if err := db.EvalQuery(sampleQuery(), col, &result); err != nil {
			fmt.Println("Query error", err)
		}
	})

	// Benchmark document update
	average("update", func() {
		if err := col.Update(ids[rand.Intn(benchSize)], sampleDoc()); err != nil {
			fmt.Println("Update error", err)
		}
	})

	// Benchmark document delete
	var delCount int64
	average("delete", func() {
		if err := col.Delete(ids[rand.Intn(benchSize)]); err == nil {
			atomic.AddInt64(&delCount, 1)
		}
	})
	fmt.Printf("Deleted %d documents\n", delCount)
}

// Document CRUD operations running in parallel, intended for catching concurrency related bugs.
func benchmark2() {
	docs := make([]int, 0, benchSize*2+1000)
	wp := new(sync.WaitGroup)
	numThreads := runtime.GOMAXPROCS(-1)
	wp.Add(5 * numThreads) // There are 5 goroutines: insert, read, query, update and delete

	// Prepare a collection with two indexes
	tmp := "/tmp/tiedot_bench2"
	if benchCleanup {
		defer os.RemoveAll(tmp)
	}
	col := mkTmpDBAndCol(tmp, "tmp")
	col.Index([]string{"nested", "nested", "str"})
	col.Index([]string{"nested", "nested", "int"})
	col.Index([]string{"nested", "nested", "float"})
	col.Index([]string{"strs"})
	col.Index([]string{"ints"})
	col.Index([]string{"floats"})

	// Insert 1000 documents to make a start
	for j := 0; j < 1000; j++ {
		if newID, err := col.Insert(sampleDoc()); err == nil {
			docs = append(docs, newID)
		} else {
			fmt.Println("Insert error", err)
		}
	}
	start := float64(time.Now().UTC().UnixNano())

	// Insert benchSize * 2 documents
	for i := 0; i < numThreads; i++ {
		go func(i int) {
			fmt.Printf("Insert thread %d starting\n", i)
			defer wp.Done()
			for j := 0; j < benchSize/numThreads*2; j++ {
				if newID, err := col.Insert(sampleDoc()); err == nil {
					docs = append(docs, newID)
				} else {
					fmt.Println("Insert error", err)
				}
			}
			fmt.Printf("Insert thread %d completed\n", i)
		}(i)
	}

	// Read benchSize * 2 documents
	var readCount int64
	for i := 0; i < numThreads; i++ {
		go func(i int) {
			fmt.Printf("Read thread %d starting\n", i)
			defer wp.Done()
			for j := 0; j < benchSize/numThreads*2; j++ {
				if _, err := col.Read(docs[rand.Intn(len(docs))]); err == nil {
					atomic.AddInt64(&readCount, 1)
				}
			}
			fmt.Printf("Read thread %d completed\n", i)
		}(i)
	}

	// Query benchSize times (lookup on two attributes)
	for i := 0; i < numThreads; i++ {
		go func(i int) {
			fmt.Printf("Query thread %d starting\n", i)
			defer wp.Done()
			var err error
			for j := 0; j < benchSize/numThreads; j++ {
				result := make(map[int]struct{})
				if err = db.EvalQuery(sampleQuery(), col, &result); err != nil {
					fmt.Println("Query error", err)
				}
			}
			fmt.Printf("Query thread %d completed\n", i)
		}(i)
	}

	// Update benchSize documents
	var updateCount int64
	for i := 0; i < numThreads; i++ {
		go func(i int) {
			fmt.Printf("Update thread %d starting\n", i)
			defer wp.Done()
			for j := 0; j < benchSize/numThreads; j++ {
				if err := col.Update(docs[rand.Intn(len(docs))], sampleDoc()); err == nil {
					atomic.AddInt64(&updateCount, 1)
				}
			}
			fmt.Printf("Update thread %d completed\n", i)
		}(i)
	}

	// Delete benchSize documents
	var delCount int64
	for i := 0; i < numThreads; i++ {
		go func(i int) {
			fmt.Printf("Delete thread %d starting\n", i)
			defer wp.Done()
			for j := 0; j < benchSize/numThreads; j++ {
				if err := col.Delete(docs[rand.Intn(len(docs))]); err == nil {
					atomic.AddInt64(&delCount, 1)
				}
			}
			fmt.Printf("Delete thread %d completed\n", i)
		}(i)
	}

	// Wait for all goroutines to finish, then print summary
	wp.Wait()
	end := float64(time.Now().UTC().UnixNano())
	fmt.Printf("Total operations %d: %d ns/iter, %d iter/sec\n", benchSize*7, int((end-start)/float64(benchSize)/7), int(1000000000/((end-start)/float64(benchSize)/7)))
	fmt.Printf("Read %d documents\n", readCount)
	fmt.Printf("Updated %d documents\n", updateCount)
	fmt.Printf("Deleted %d documents\n", delCount)
}
