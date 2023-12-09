package main

import (
	"image"
	"math/rand"
	"testing"

	"golang.org/x/image/draw"
)

func createSubImage(img image.Image, r image.Rectangle) image.Image {
	subimg := image.NewRGBA(r)
	draw.Draw(subimg, r, img, r.Min, draw.Src)
	return subimg
}

func TestFindImage(t *testing.T) {
	// Create test images
	imgsrc, err := openImage("test/img/haystack2.jpg")
	if err != nil {
		t.Fatal(err)
	}

	rect := image.Rect(66, 287, 121, 327)

	subsrc := createSubImage(imgsrc, rect)

	// Define test options
	opts := Opts{
		imgMinWidth: 8,
		imgMaxWidth: 128,
		subMinArea:  5 * 5,
		verbose:     true,
	}

	// Find image
	matches := findImage(imgsrc, subsrc, opts)
	if err != nil {
		t.Error(err)
	}

	// Check results
	if len(matches) < 1 {
		t.Fatal("No matches found")
	}

	itr := matches[0].Bounds.Intersect(rect)
	if itr.Empty() {
		t.Fatal("No intersection found")
	}

	if itr.Dx() < 5 || itr.Dy() < 5 {
		t.Fatal("Intersection too small")
	}

	println("Found match:", matches[0].Bounds.String())
}

func TestFindImageRandom(t *testing.T) {
	// Create test images
	imgsrc, err := openImage("test/img/haystack2.jpg")
	if err != nil {
		t.Fatal(err)
	}
	bounds := imgsrc.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	rnd := rand.New(rand.NewSource(0))

	for i := 0; i < 10; i++ {
		// Create random rectangle
		x := rnd.Intn(w - 1)
		y := rnd.Intn(h - 1)
		sw := rnd.Intn(w-1-x) + 1
		sh := rnd.Intn(h-1-y) + 1
		rect := image.Rect(x, y, x+sw, y+sh)

		subsrc := createSubImage(imgsrc, rect)

		// Define test options
		opts := Opts{
			imgMinWidth: 8,
			imgMaxWidth: 128,
			subMinArea:  5 * 5,
			k:           1,
			verbose:     true,
		}

		// Find image
		matches := findImage(imgsrc, subsrc, opts)
		if err != nil {
			t.Error(err)
			continue
		}

		// Check results
		if len(matches) < 1 {
			t.Error("No matches found")
			continue
		}

		itr := matches[0].Bounds.Intersect(rect)
		if itr.Empty() {
			t.Errorf("No intersection found: %s", matches[0].Bounds.String())
			continue
		}

		if itr.Dx() < 5 || itr.Dy() < 5 {
			t.Error("Intersection too small")
			continue
		}
		println("Found match:", matches[0].Bounds.String())
	}
}

func TestFindImageRandomPatches(t *testing.T) {
	// Create test images
	imgsrc, err := openImage("test/img/haystack2.jpg")
	if err != nil {
		t.Fatal(err)
	}
	bounds := imgsrc.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	rnd := rand.New(rand.NewSource(0))

	for i := 0; i < 30; i++ {
		// Create random rectangle
		sw := 200
		sh := 150
		x := rnd.Intn(w - 1 - sw)
		y := rnd.Intn(h - 1 - sh)
		rect := image.Rect(x, y, x+sw, y+sh)

		subsrc := createSubImage(imgsrc, rect)

		// Define test options
		opts := Opts{
			k: 1,
			// html:        true,
			// convolution: true,
			// visualize:   true,
		}

		// Find image
		matches := findImage(imgsrc, subsrc, opts)
		if err != nil {
			t.Error(err)
			continue
		}

		// Check results
		if len(matches) < 1 {
			t.Error("No matches found")
			continue
		}

		itr := matches[0].Bounds.Intersect(rect)
		if itr.Empty() {
			t.Errorf("No intersection found: expected %s got %s", rect.String(), matches[0].Bounds.String())
			continue
		}

		if itr.Dx() < 5 || itr.Dy() < 5 {
			t.Error("Intersection too small")
			continue
		}
		t.Log("Found match:", matches[0].Bounds.String())
	}
}

func BenchmarkFindImageRandomPatches(b *testing.B) {
	// Create test images
	imgsrc, err := openImage("test/img/haystack2.jpg")
	if err != nil {
		b.Fatal(err)
	}
	bounds := imgsrc.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	rnd := rand.New(rand.NewSource(0))

	for i := 0; i < b.N; i++ {
		b.StopTimer()

		// Create random rectangle
		sw := 200
		sh := 150
		x := rnd.Intn(w - 1 - sw)
		y := rnd.Intn(h - 1 - sh)
		rect := image.Rect(x, y, x+sw, y+sh)

		subsrc := createSubImage(imgsrc, rect)

		// Define test options
		opts := Opts{
			k: 1,
		}

		// Find image
		b.StartTimer()
		matches := findImage(imgsrc, subsrc, opts)
		b.StopTimer()
		if err != nil {
			b.Error(err)
			continue
		}

		// Check results
		if len(matches) < 1 {
			b.Error("No matches found")
			continue
		}

		itr := matches[0].Bounds.Intersect(rect)
		if itr.Empty() {
			b.Errorf("No intersection found: expected %s got %s", rect.String(), matches[0].Bounds.String())
			continue
		}

		if itr.Dx() < 5 || itr.Dy() < 5 {
			b.Error("Intersection too small")
			continue
		}
		b.Log("Found match:", matches[0].Bounds.String())
	}
}

func FuzzFindImage(f *testing.F) {

	// Create test images
	imgsrc, err := openImage("test/img/haystack2.jpg")
	if err != nil {
		f.Fatal(err)
	}

	f.Add(66, 287, 121, 327)
	f.Fuzz(func(t *testing.T, a int, b int, c int, d int) {
		rect := image.Rect(a, b, c, d)

		subsrc := createSubImage(imgsrc, rect)

		// Define test options
		opts := Opts{
			imgMinWidth: 8,
			imgMaxWidth: 128,
			subMinArea:  5 * 5,
		}

		// Find image
		matches := findImage(imgsrc, subsrc, opts)
		if err != nil {
			t.Error(err)
		}

		// Check results
		if len(matches) < 1 {
			t.Error("No matches found")
		}

		itr := matches[0].Bounds.Intersect(rect)
		if itr.Empty() {
			t.Error("No intersection found")
		}

		if itr.Dx() < 5 || itr.Dy() < 5 {
			t.Error("Intersection too small")
		}

		println("Found match:", matches[0].Bounds.String())
	})
}
