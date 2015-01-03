package offheap

import (
	"fmt"
	"unsafe"
)

// based on the public domain code of https://github.com/preshing/CompareIntegerMaps

//----------------------------------------------
//  HashTable
//
//  Maps pointer-sized integers to pointer-sized integers.
//  Uses open addressing with linear probing.
//  In the t.cells array, Key = 0 is reserved to indicate an unused cell.
//  Actual value for key 0 (if any) is stored in t.zeroCell.
//  The hash table automatically doubles in size when it becomes 75% full.
//  The hash table never shrinks in size, even after Clear(), unless you explicitly call Compact().
//----------------------------------------------

type Cell struct {
	Key   uint64
	Value interface{}
}

type HashTable struct {
	cells      []Cell
	arraySize  uint64
	population uint64
	zeroUsed   bool
	zeroCell   Cell
}

func NewHashTable(initialSize uint64) *HashTable {
	return &HashTable{
		// todo: allocate this off-heap instead
		cells:     make([]Cell, initialSize),
		arraySize: initialSize,
	}
}

func (t *HashTable) DestroyHashTable() {
	// todo: release the off-heap allocation here
}

// Basic operations
func (t *HashTable) Lookup(key uint64) *Cell {

	var cell *Cell

	if key == 0 {
		if t.zeroUsed {
			return &t.zeroCell
		}
		return nil

	} else {

		h := integerHash(uint64(key)) % t.arraySize

		for {
			cell = &(t.cells[h])
			if cell.Key == key {
				return cell
			}
			if cell.Key == 0 {
				return nil
			}
			h++
			if h == t.arraySize {
				h = 0
			}
		}
	}
}

// 2nd return value is false if already existed (and thus took no action)
func (t *HashTable) Insert(key uint64) (*Cell, bool) {

	var cell *Cell

	if key != 0 {

		for {
			h := integerHash(uint64(key)) % t.arraySize

			for {
				cell = &(t.cells[h])

				if cell.Key == key {
					// already exists
					return cell, false
				}
				if cell.Key == 0 {
					if (t.population+1)*4 >= t.arraySize*3 {
						t.Repopulate(t.arraySize * 2)
						// resized, so start all over
						break
					}
					t.population++
					cell.Key = key
					return cell, true
				}

				h++
				if h == t.arraySize {
					h = 0
				}

			}
		}
	} else {

		if !t.zeroUsed {

			t.zeroUsed = true
			t.population++
			if t.population*4 >= t.arraySize*3 {

				t.Repopulate(t.arraySize * 2)
			}
		}
		return &t.zeroCell, true
	}

}

func (t *HashTable) DeleteCell(cell *Cell) {

	if cell == &t.zeroCell {
		// Delete zero cell
		if !t.zeroUsed {
			panic("deleting zero element when not used")
		}
		t.zeroUsed = false
		cell.Value = nil
		t.population--
		return

	} else {

		pos := uint64((uintptr(unsafe.Pointer(cell)) - uintptr(unsafe.Pointer(&t.cells[0]))) / uintptr(unsafe.Sizeof(Cell{})))

		// Delete from regular cells
		if pos < 0 || pos >= t.arraySize {
			panic(fmt.Sprintf("cell out of bounds: pos %v was < 0 or >= t.arraySize == %v", pos, t.arraySize))
		}
		if t.cells[pos].Key == 0 {
			panic("zero Key in non-zero Cell!")
		}

		// Remove this cell by shuffling neighboring cells so there are no gaps in anyone's probe chain
		nei := pos + 1
		if nei >= t.arraySize {
			nei = 0
		}
		var neighbor *Cell
		var circular_offset_ideal_pos int64
		var circular_offset_ideal_nei int64

		for {
			neighbor = &t.cells[nei]

			if neighbor.Key == 0 {
				// There's nobody to swap with. Go ahead and clear this cell, then return
				t.cells[pos].Key = 0
				t.cells[pos].Value = nil
				t.population--
				return
			}

			ideal := integerHash(neighbor.Key) % t.arraySize

			if pos >= ideal {
				circular_offset_ideal_pos = int64(pos) - int64(ideal)
			} else {
				// pos < ideal, so pos - ideal is negative, wrap-around has happened.
				circular_offset_ideal_pos = int64(t.arraySize) - int64(ideal) + int64(pos)
			}

			if nei >= ideal {
				circular_offset_ideal_nei = int64(nei) - int64(ideal)
			} else {
				// nei < ideal, so nei - ideal is negative, wrap-around has happened.
				circular_offset_ideal_nei = int64(t.arraySize) - int64(ideal) + int64(nei)
			}

			if circular_offset_ideal_pos < circular_offset_ideal_nei {
				// Swap with neighbor, then make neighbor the new cell to remove.
				t.cells[pos] = *neighbor
				pos = nei
			}

			nei++
			if nei >= t.arraySize {
				nei = 0
			}
		}
	}

}

func (t *HashTable) Clear() {
	// (Does not resize the array)
	// Clear regular cells

	// todo, change to use off heap memory
	for i := range t.cells {
		t.cells[i] = Cell{}
	}
	t.population = 0

	// Clear zero cell
	t.zeroUsed = false
	t.zeroCell.Value = 0
}

func (t *HashTable) Compact() {
	t.Repopulate(upper_power_of_two((t.population*4 + 3) / 3))
}

func (t *HashTable) DeleteKey(key uint64) {
	value := t.Lookup(key)
	if value != nil {
		t.DeleteCell(value)
	}
}

func (t *HashTable) Repopulate(desiredSize uint64) {

	if desiredSize&(desiredSize-1) != 0 {
		panic("desired size must be a power of 2")
	}
	if t.population*4 > desiredSize*3 {
		panic("must have t.population * 4  <= desiredSize * 3")
	}

	// Get start/end pointers of old array
	oldCells := t.cells

	// Allocate new array
	t.arraySize = desiredSize
	t.cells = make([]Cell, t.arraySize)

	// Iterate through old array
	// (any zero entry can stay in place; so ignore Key == 0 below).
	var c *Cell
	var pos uint64
	for i := range oldCells {
		{
			c = &oldCells[i]
			if c.Key != 0 {
				// Insert this element into new array
				pos = integerHash(c.Key) % t.arraySize

				// for ;; cell = ((cell) + 1 != t.cells + t.arraySize ? (cell) + 1 : t.cells))
				// for (Cell* cell = FIRST_CELL(integerHash(c.Key));; cell = CIRCULAR_NEXT(cell))

				for {
					cell := &t.cells[pos]

					if cell.Key != 0 {
						// Insert here
						*cell = *c
						break
					}
					pos++
					if pos >= t.arraySize {
						pos = 0
					}
				}
			}
		}

		// Delete old array; happens when oldCells goes out of scope
		// todo: delete in off-heap space
	}
}

//----------------------------------------------
//  Iterator
//----------------------------------------------

type Iterator struct {
	Tab *HashTable
	Pos int64
	Cur *Cell // nil when done
}

func NewIterator(tab *HashTable) *Iterator {
	it := &Iterator{
		Tab: tab,
		Cur: &tab.zeroCell,
	}

	if !it.Tab.zeroUsed {
		it.Next()
	}

	return it
}

func (it *Iterator) Next() *Cell {

	// Already finished?
	if it.Cur == nil {
		return nil
	}

	// Iterate past zero cell
	if it.Cur == &it.Tab.zeroCell {
		it.Pos = -1
	}

	// Iterate through the regular cells
	it.Pos++
	for uint64(it.Pos) != it.Tab.arraySize {
		it.Cur = &it.Tab.cells[it.Pos]
		if it.Cur.Key != 0 {
			return it.Cur
		}
		it.Pos++
	}

	// Finished
	it.Cur = nil
	return nil
}
