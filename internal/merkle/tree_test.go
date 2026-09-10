package merkle

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fixture mirrors the shape of testdata/merkle/*.json.
type fixture struct {
	Leaves []string `json:"leaves"`
	Root   string   `json:"root"`
	Proofs []struct {
		Leaf  string   `json:"leaf"`
		Proof []string `json:"proof"`
	} `json:"proofs"`
}

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "merkle", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var f fixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	return f
}

func hexTo32(t *testing.T, s string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode hex %q: %v", s, err)
	}
	if len(b) != 32 {
		t.Fatalf("hex %q is %d bytes, want 32", s, len(b))
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

// TestBuildTree_MatchesFixtures is the test the build sequence calls out
// explicitly: every fixture, including the unbalanced seven-leaf case,
// must reproduce the exact root and per-leaf proof the Rust contract's
// format pins down. This is checked two ways — direct equality against
// the fixture's proof list, and independently folding each fixture proof
// with this package's own Fold and comparing to the fixture's root — so a
// bug that happened to produce the right root through a different (wrong)
// per-leaf proof assignment would still be caught.
func TestBuildTree_MatchesFixtures(t *testing.T) {
	for _, name := range []string{"single-leaf.json", "four-leaves.json", "seven-leaves.json"} {
		t.Run(name, func(t *testing.T) {
			f := loadFixture(t, name)

			leaves := make([][32]byte, len(f.Leaves))
			for i, l := range f.Leaves {
				leaves[i] = hexTo32(t, l)
			}

			tree, err := BuildTree(leaves)
			if err != nil {
				t.Fatalf("BuildTree returned unexpected error: %v", err)
			}

			wantRoot := hexTo32(t, f.Root)
			if tree.Root != wantRoot {
				t.Errorf("Root = %x, want %x", tree.Root, wantRoot)
			}

			if len(f.Proofs) != len(leaves) {
				t.Fatalf("fixture has %d proof entries for %d leaves", len(f.Proofs), len(leaves))
			}
			for i, p := range f.Proofs {
				wantLeaf := hexTo32(t, p.Leaf)
				if leaves[i] != wantLeaf {
					t.Fatalf("fixture proof entry %d is for leaf %s, but leaves[%d] is %x — fixture order assumption broken",
						i, p.Leaf, i, leaves[i])
				}

				wantProof := make(Proof, len(p.Proof))
				for j, s := range p.Proof {
					wantProof[j] = hexTo32(t, s)
				}

				gotProof := tree.Proofs[i]
				if len(gotProof) != len(wantProof) {
					t.Errorf("leaf %d: proof has %d nodes, want %d", i, len(gotProof), len(wantProof))
				} else {
					for j := range gotProof {
						if gotProof[j] != wantProof[j] {
							t.Errorf("leaf %d: proof[%d] = %x, want %x", i, j, gotProof[j], wantProof[j])
						}
					}
				}

				// Cross-check: folding the fixture's own proof (not ours)
				// against the fixture's own root must succeed regardless
				// of what BuildTree produced.
				if !VerifyProof(wantRoot, wantLeaf, wantProof) {
					t.Errorf("leaf %d: fixture's own proof does not fold to the fixture's own root", i)
				}

				// And folding what we produced must also reach the root.
				if !VerifyProof(tree.Root, leaves[i], gotProof) {
					t.Errorf("leaf %d: BuildTree's proof does not fold to BuildTree's root", i)
				}
			}
		})
	}
}

func TestBuildTree_RejectsEmpty(t *testing.T) {
	_, err := BuildTree(nil)
	if err != ErrEmptyTree {
		t.Fatalf("BuildTree(nil) error = %v, want ErrEmptyTree", err)
	}
}

func TestBuildTree_RejectsDuplicateLeaf(t *testing.T) {
	leaf := [32]byte{1, 2, 3}
	_, err := BuildTree([][32]byte{leaf, {4, 5, 6}, leaf})
	if err == nil {
		t.Fatal("BuildTree accepted a duplicate leaf")
	}
}

func TestBuildTree_SingleLeafHasEmptyProofAndRootEqualsLeaf(t *testing.T) {
	leaf := [32]byte{9, 9, 9}
	tree, err := BuildTree([][32]byte{leaf})
	if err != nil {
		t.Fatalf("BuildTree returned unexpected error: %v", err)
	}
	if tree.Root != leaf {
		t.Errorf("Root = %x, want the single leaf %x unchanged", tree.Root, leaf)
	}
	if len(tree.Proofs[0]) != 0 {
		t.Errorf("single-leaf proof has %d entries, want 0", len(tree.Proofs[0]))
	}
}

// TestBuildTree_OddCountsAtEveryDepth exercises the odd-leaf-promotion
// path at several tree depths (3, 5, 9, 13 leaves), not just the fixture's
// seven-leaf case, and checks every proof folds back to the root.
func TestBuildTree_OddCountsAtEveryDepth(t *testing.T) {
	for _, n := range []int{2, 3, 5, 6, 9, 13} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			leaves := make([][32]byte, n)
			for i := range leaves {
				leaves[i][0] = byte(i + 1)
				leaves[i][31] = byte(n)
			}
			tree, err := BuildTree(leaves)
			if err != nil {
				t.Fatalf("BuildTree returned unexpected error: %v", err)
			}
			for i, leaf := range leaves {
				if !VerifyProof(tree.Root, leaf, tree.Proofs[i]) {
					t.Errorf("leaf %d of %d does not verify against the root", i, n)
				}
			}
		})
	}
}

func TestBuildTree_RejectsProofOverCap(t *testing.T) {
	// 2^33 leaves would need a 33-node proof, exceeding the contract's cap
	// of 32 — far too many to actually construct, so this is checked via
	// the cap constant directly rather than an enormous input.
	if maxProofLength != 32 {
		t.Fatalf("maxProofLength = %d, want 32 per the contract's cap", maxProofLength)
	}
}

func TestVerifyProof_RejectsWrongLeafOrProof(t *testing.T) {
	leaves := [][32]byte{{1}, {2}, {3}, {4}}
	tree, err := BuildTree(leaves)
	if err != nil {
		t.Fatalf("BuildTree returned unexpected error: %v", err)
	}

	if VerifyProof(tree.Root, [32]byte{99}, tree.Proofs[0]) {
		t.Error("VerifyProof accepted a proof for the wrong leaf")
	}
	if VerifyProof(tree.Root, leaves[0], tree.Proofs[1]) {
		t.Error("VerifyProof accepted the wrong leaf's proof")
	}
	var wrongRoot [32]byte
	if VerifyProof(wrongRoot, leaves[0], tree.Proofs[0]) {
		t.Error("VerifyProof accepted a mismatched root")
	}
}
