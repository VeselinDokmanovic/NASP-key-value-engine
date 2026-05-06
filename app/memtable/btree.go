package memtable

import (
	"bytes"
)

type BTreeNode struct {
	Keys     []*Entry
	Children []*BTreeNode
	IsLeaf   bool
}

func NewBTreeNode(leaf bool) *BTreeNode {
	return &BTreeNode{
		Keys:     make([]*Entry, 0),
		Children: make([]*BTreeNode, 0),
		IsLeaf:   leaf,
	}
}

type BTreeMemtable struct {
	Root       *BTreeNode
	Order      int
	size       int
	memoryUsed int64
	cfg        MemtableConfig
}

func NewBTreeMemtable(cfg MemtableConfig) *BTreeMemtable {
	order := cfg.BTreeOrder
	if order < 3 {
		order = 3
	}
	return &BTreeMemtable{
		Root:  NewBTreeNode(true),
		Order: order,
		cfg:   cfg,
	}
}

func (b *BTreeMemtable) Put(key []byte, entry *Entry) bool {
	if b.IsFull() {
		return false
	}

	root := b.Root
	if len(root.Keys) == b.Order-1 {
		newRoot := NewBTreeNode(false)
		newRoot.Children = append(newRoot.Children, root)
		b.splitChild(newRoot, 0)
		b.Root = newRoot
	}

	return b.insertNonFull(b.Root, key, entry)
}

func (b *BTreeMemtable) splitChild(parent *BTreeNode, i int) {
	order := b.Order
	mid := (order - 1) / 2
	fullNode := parent.Children[i]

	newNode := NewBTreeNode(fullNode.IsLeaf)

	midEntry := fullNode.Keys[mid]

	newNode.Keys = append(newNode.Keys, fullNode.Keys[mid+1:]...)
	fullNode.Keys = fullNode.Keys[:mid]

	if !fullNode.IsLeaf {
		newNode.Children = append(newNode.Children, fullNode.Children[mid+1:]...)
		fullNode.Children = fullNode.Children[:mid+1]
	}

	parent.Children = append(parent.Children, nil)
	copy(parent.Children[i+2:], parent.Children[i+1:])
	parent.Children[i+1] = newNode

	parent.Keys = append(parent.Keys, nil)
	copy(parent.Keys[i+1:], parent.Keys[i:])
	parent.Keys[i] = midEntry
}

func (b *BTreeMemtable) insertNonFull(node *BTreeNode, key []byte, entry *Entry) bool {
	i := len(node.Keys) - 1

	for i >= 0 {
		cmp := bytes.Compare(key, node.Keys[i].Key)
		if cmp == 0 {
			b.memoryUsed -= int64(len(node.Keys[i].Key) + len(node.Keys[i].Value) + 9)
			node.Keys[i] = entry
			b.memoryUsed += int64(len(key) + len(entry.Value) + 9)
			return true
		}
		if cmp < 0 {
			i--
		} else {
			break
		}
	}
	i++

	if node.IsLeaf {
		node.Keys = append(node.Keys, nil)
		copy(node.Keys[i+1:], node.Keys[i:])
		node.Keys[i] = entry
		b.size++
		b.memoryUsed += int64(len(key) + len(entry.Value) + 9)
		return true
	} else {
		if len(node.Children[i].Keys) == b.Order-1 {
			b.splitChild(node, i)
			if bytes.Compare(key, node.Keys[i].Key) > 0 {
				i++
			}
		}
		return b.insertNonFull(node.Children[i], key, entry)
	}
}

func (b *BTreeMemtable) Get(key []byte) (*Entry, bool) {
	return b.search(b.Root, key)
}

func (b *BTreeMemtable) search(node *BTreeNode, key []byte) (*Entry, bool) {
	i := 0
	for i < len(node.Keys) && bytes.Compare(key, node.Keys[i].Key) > 0 {
		i++
	}

	if i < len(node.Keys) && bytes.Equal(key, node.Keys[i].Key) {
		if node.Keys[i].Tombstone != 0 {
			return node.Keys[i], false
		}
		return node.Keys[i], true
	}

	if node.IsLeaf {
		return nil, false
	}

	return b.search(node.Children[i], key)
}

func (b *BTreeMemtable) Delete(key []byte) bool {
	entry, exists := b.Get(key)
	if !exists {
		tombstone := &Entry{Key: key, Value: nil, Tombstone: 1, Type: EntryTypeDelete}
		return b.Put(key, tombstone)
	}
	entry.Tombstone = 1
	entry.Type = EntryTypeDelete
	return true
}

func (b *BTreeMemtable) GetAllSorted() []*Entry {
	result := make([]*Entry, 0, b.size)
	b.inOrder(b.Root, &result)
	return result
}

func (b *BTreeMemtable) inOrder(node *BTreeNode, result *[]*Entry) {
	if node == nil {
		return
	}
	for i := 0; i < len(node.Keys); i++ {
		if !node.IsLeaf {
			b.inOrder(node.Children[i], result)
		}
		*result = append(*result, node.Keys[i])
	}
	if !node.IsLeaf {
		b.inOrder(node.Children[len(node.Keys)], result)
	}
}

func (b *BTreeMemtable) IsFull() bool {
	return b.size >= b.cfg.MaxEntries || b.memoryUsed >= b.cfg.MaxMemoryKB*1024
}

func (b *BTreeMemtable) Size() int { return b.size }

func (b *BTreeMemtable) MemoryUsage() int { return int(b.memoryUsed) }

func (b *BTreeMemtable) Clear() {
	b.Root = NewBTreeNode(true)
	b.size = 0
	b.memoryUsed = 0
}
