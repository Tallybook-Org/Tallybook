package merkle

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
)

// maxProofLength is the contract's cap on proof length (§4). BuildTree
// refuses to produce a tree that would need a longer proof for any leaf.
const maxProofLength = 32

// ErrEmptyTree is returned by BuildTree when given no leaves.
var ErrEmptyTree = errors.New("merkle: cannot build a tree from zero leaves")

// Proof is an ordered list of sibling hashes: fold(leaf, proof) reproduces
// the tree's root when leaf and proof are consistent with it. There is no
// leaf index — the contract's fold function doesn't take one either (§4) —
// so a proof only proves membership of its leaf value, not a position.
type Proof [][32]byte

// Tree is a sorted-pair merkle tree over 32-byte leaves, built to match the
// statement_registry contract's fold algorithm exactly: an internal node is
// sha256(min(a,b) || max(a,b)) by byte order, and a leaf or subtree left
// unpaired at a level is promoted unchanged to the next level rather than
// being paired with itself. This is the algorithm the merkle testdata
// fixtures pin down — see tree_test.go.
type Tree struct {
	Root [32]byte
	// Proofs[i] is the proof for the leaf passed to BuildTree at index i.
	Proofs []Proof
}

// BuildTree builds a Tree over leaves, in the order given. It rejects an
// empty leaf set, a duplicate leaf value (ambiguous under a proof format
// with no leaf index — two occurrences of the same 32 bytes are
// indistinguishable once folded), and any leaf whose resulting proof would
// exceed the contract's 32-node cap.
func BuildTree(leaves [][32]byte) (*Tree, error) {
	if len(leaves) == 0 {
		return nil, ErrEmptyTree
	}

	seen := make(map[[32]byte]int, len(leaves))
	for i, l := range leaves {
		if prior, ok := seen[l]; ok {
			return nil, fmt.Errorf("merkle: duplicate leaf at indices %d and %d", prior, i)
		}
		seen[l] = i
	}

	// groups[i] holds the original leaf indices folded into level[i], so a
	// sibling encountered while building the tree can be appended to every
	// original leaf's proof that descends from it.
	level := append([][32]byte(nil), leaves...)
	groups := make([][]int, len(leaves))
	for i := range groups {
		groups[i] = []int{i}
	}
	proofs := make([]Proof, len(leaves))

	for len(level) > 1 {
		nextLevel := make([][32]byte, 0, (len(level)+1)/2)
		nextGroups := make([][]int, 0, (len(level)+1)/2)

		for i := 0; i < len(level); {
			if i+1 < len(level) {
				a, b := level[i], level[i+1]
				for _, idx := range groups[i] {
					proofs[idx] = append(proofs[idx], b)
				}
				for _, idx := range groups[i+1] {
					proofs[idx] = append(proofs[idx], a)
				}
				nextLevel = append(nextLevel, sortedPairHash(a, b))
				nextGroups = append(nextGroups, append(groups[i], groups[i+1]...))
				i += 2
			} else {
				// Odd one out: promoted unchanged, no proof entry added.
				nextLevel = append(nextLevel, level[i])
				nextGroups = append(nextGroups, groups[i])
				i++
			}
		}

		level = nextLevel
		groups = nextGroups
	}

	for i, p := range proofs {
		if len(p) > maxProofLength {
			return nil, fmt.Errorf("merkle: proof for leaf %d has %d nodes, exceeds contract cap of %d",
				i, len(p), maxProofLength)
		}
	}

	return &Tree{Root: level[0], Proofs: proofs}, nil
}

// Fold reproduces the contract's verification algorithm: starting from
// leaf, it folds in each proof element with sorted-pair hashing and returns
// the resulting root candidate.
func Fold(leaf [32]byte, proof Proof) [32]byte {
	node := leaf
	for _, sibling := range proof {
		node = sortedPairHash(node, sibling)
	}
	return node
}

// VerifyProof reports whether folding leaf through proof reproduces root —
// a local, Go-side mirror of what statement_registry.verify_usage checks
// on chain.
func VerifyProof(root, leaf [32]byte, proof Proof) bool {
	return Fold(leaf, proof) == root
}

func sortedPairHash(a, b [32]byte) [32]byte {
	if bytes.Compare(a[:], b[:]) <= 0 {
		return sha256.Sum256(append(append([]byte{}, a[:]...), b[:]...))
	}
	return sha256.Sum256(append(append([]byte{}, b[:]...), a[:]...))
}
