package main

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"log"
	"math"
	"math/rand"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"

	"golang.org/x/image/draw"
)

type Run struct {
	Size    image.Point
	Subruns []Subrun
}

type Subrun struct {
	Image       image.Image
	Selected    bool
	Skipped     bool
	Reason      string
	Subimage    image.Image
	Convolution image.Image
	Visualized  image.Image
	Matches     []Match
}

type Matches []Match

func (m Matches) Scale(scale float64) Matches {
	for i := range m {
		m[i] = m[i].Scale(scale)
	}
	return m
}

func (m Match) Scale(scale float64) Match {
	m.Bounds = image.Rectangle{
		Min: image.Point{
			X: int(float64(m.Bounds.Min.X) * scale),
			Y: int(float64(m.Bounds.Min.Y) * scale),
		},
		Max: image.Point{
			X: int(float64(m.Bounds.Max.X) * scale),
			Y: int(float64(m.Bounds.Max.Y) * scale),
		},
	}
	return m
}

func (r Run) PrintHTML(t *template.Template) {
	err := t.Execute(os.Stdout, r)
	if err != nil {
		log.Fatalf("failed to execute template: %v", err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: findimg [options] <image> <subimage>\n")
	flag.PrintDefaults()
	os.Exit(2)
}

var (
	output      = flag.String("o", "", "result output format (json, html, text)")
	random      = flag.Bool("random", false, "randomly pick subimage as test")
	verbose     = flag.Bool("v", false, "verbose output")
	cpuProfile  = flag.String("cpu-profile", "", "write cpu profile to file")
	imgMinWidth = flag.Int("img-min-width", 0, "minimum image width")
	imgMaxWidth = flag.Int("img-max-width", 0, "maximum image width")
	subMinArea  = flag.Int("sub-min-area", 0, "minimum subimage area")
	subMaxDiv   = flag.Int("sub-max-div", 0, "maximum subimage division")
	k           = flag.Int("k", 0, "number of top matches to keep")
)

type Opts struct {
	imgMinWidth int
	imgMaxWidth int
	subMinArea  int
	subMaxDiv   int
	k           int
	html        bool
	verbose     bool
	convolution bool
	visualize   bool
	runTmpl     *template.Template
}

var DEFAULT_OPTS = Opts{
	k:           6,
	imgMinWidth: 8,
	imgMaxWidth: 256,
	subMaxDiv:   64,
	subMinArea:  5 * 5,
	html:        false,
	verbose:     false,
}

//go:embed templates/*.html
var templatesFS embed.FS
var templates struct {
	header *template.Template
	footer *template.Template
	run    *template.Template
}
var templatesOnce sync.Once

func main() {
	log.SetFlags(0)
	log.SetPrefix("findimg: ")

	flag.Usage = usage
	flag.Parse()

	imgPath := flag.Arg(0)
	subimgPath := flag.Arg(1)

	if imgPath == "" || (subimgPath == "" && !*random) {
		usage()
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}

	// Open the input images
	imgsrc, err := openImage(imgPath)
	if err != nil {
		log.Fatalf("failed to open image: %v", err)
	}

	var subsrc image.Image
	if *random {
		subsrc = randomSubimage(imgsrc)
	} else {
		subsrc, err = openImage(subimgPath)
		if err != nil {
			log.Fatalf("failed to open image: %v", err)
		}
	}

	opts := Opts{}
	opts.html = *output == "html"
	opts.verbose = *verbose
	opts.imgMinWidth = *imgMinWidth
	opts.imgMaxWidth = *imgMaxWidth
	opts.subMinArea = *subMinArea

	if opts.html {
		opts.convolution = true
		opts.visualize = true
	}

	matches := findImage(imgsrc, subsrc, opts)

	switch *output {
	case "json":
		json.NewEncoder(os.Stdout).Encode(matches)
	case "html":
	default:
		for _, match := range matches {
			fmt.Printf("%6f %4d %4d %4d %4d\n", match.Match, match.Bounds.Min.X, match.Bounds.Min.Y, match.Bounds.Max.X, match.Bounds.Max.Y)
		}
	}
}

func findImage(imgsrc image.Image, subsrc image.Image, opts Opts) []Match {

	if opts.imgMinWidth == 0 {
		opts.imgMinWidth = DEFAULT_OPTS.imgMinWidth
	}

	if opts.imgMaxWidth == 0 {
		opts.imgMaxWidth = DEFAULT_OPTS.imgMaxWidth
	}

	if opts.subMinArea == 0 {
		opts.subMinArea = DEFAULT_OPTS.subMinArea
	}

	if opts.subMaxDiv == 0 {
		opts.subMaxDiv = DEFAULT_OPTS.subMaxDiv
	}

	if opts.k == 0 {
		opts.k = DEFAULT_OPTS.k
	}

	if imgsrc.Bounds().Max.X < opts.imgMaxWidth {
		opts.imgMaxWidth = imgsrc.Bounds().Max.X
	}

	templatesOnce.Do(func() {
		funcs := template.FuncMap{
			"imgsrc": func(img image.Image) template.URL {
				if img == nil {
					return template.URL("")
				}
				return template.URL(fmt.Sprintf("data:image/png;base64,%s", pngb64(img)))
			},
			"dim": func(img image.Image) string {
				if img == nil {
					return "0x0"
				}
				bounds := img.Bounds()
				return fmt.Sprintf("%dx%d", bounds.Dx(), bounds.Dy())
			},
			"probalpha": func(prob float64) float64 {
				return math.Max(0, 1-(1-prob)*10)
			},
		}

		templates.run = template.Must(template.
			New("run.html").
			Funcs(funcs).
			ParseFS(templatesFS, "templates/run.html"),
		)

		templates.header = template.Must(template.
			New("header.html").
			Funcs(funcs).
			ParseFS(templatesFS, "templates/header.html"),
		)

		templates.footer = template.Must(template.
			New("footer.html").
			Funcs(funcs).
			ParseFS(templatesFS, "templates/footer.html"),
		)
	})

	if opts.html {
		templates.header.Execute(os.Stdout, struct {
			Image    image.Image
			Subimage image.Image
		}{
			Image:    imgsrc,
			Subimage: subsrc,
		})
	}

	var matches []Match

	for imgWidth := opts.imgMinWidth; imgWidth <= opts.imgMaxWidth; imgWidth *= 2 {
		img := resizeImage(imgsrc, imgWidth, 0)
		imgHeight := img.Bounds().Max.Y
		imgScale := float64(imgWidth) / float64(imgsrc.Bounds().Max.X)

		// matches = nil
		// needle = nil
		lastTopMatch := 0.0
		// var matches []Match
		// var subimgScale float64

		run := Run{
			Size: image.Point{X: imgWidth, Y: imgHeight},
		}

		done := false

		for div := 1; div <= opts.subMaxDiv; div *= 2 {
			sscale := 1.0 / float64(div)
			sw := int(float64(subsrc.Bounds().Dx()) * sscale * imgScale)
			sh := int(float64(subsrc.Bounds().Dy()) * sscale * imgScale)
			sarea := sw * sh
			if sarea < opts.subMinArea || sw >= imgWidth || sh >= imgHeight {
				if opts.verbose {
					log.Printf("image size: %dx%d, subimage size: %dx%d, div: %d, skipping\n", imgWidth, imgHeight, sw, sh, div)
				}
				break
			}

			subimg := resizeImage(subsrc, sw, sh)
			subrun := Subrun{
				Image:    img,
				Subimage: subimg,
			}

			if opts.convolution {
				subrun.Convolution = convolutionParallel(img, subimg)
			}

			divMatches := convolutionTopKParallel(img, subimg, opts.k)
			if len(divMatches) == 0 {
				subrun.Skipped = true
				subrun.Reason = "no matches"
				run.Subruns = append(run.Subruns, subrun)
				break
			}

			divTopMatch := divMatches[0]
			if opts.verbose {
				log.Printf("image size: %dx%d, subimage size: %dx%d, div: %d, match: %f %v\n", imgWidth, imgHeight, sw, sh, div, divTopMatch.Match, divTopMatch.Bounds)
			}
			if opts.visualize {
				subrun.Visualized = visualizeMatches(img, divMatches)
			}

			subrun.Matches = divMatches.Scale(1 / imgScale)
			run.Subruns = append(run.Subruns, subrun)

			if divTopMatch.Match < lastTopMatch {
				run.Subruns[len(run.Subruns)-2].Selected = true
				done = true
				break
			}
			lastTopMatch = divTopMatch.Match
			matches = divMatches
		}

		if opts.html {
			run.PrintHTML(templates.run)
		}

		if done {
			break
		}
	}

	if opts.html {
		templates.footer.Execute(os.Stdout, nil)
	}

	return matches
}

func randomSubimage(img image.Image) image.Image {
	bounds := img.Bounds()
	w := bounds.Max.X
	h := bounds.Max.Y
	x := rand.Intn(w)
	y := rand.Intn(h)
	sw := rand.Intn(w-x) + 1
	sh := rand.Intn(h-y) + 1
	subimg := image.NewRGBA(image.Rect(0, 0, sw, sh))
	draw.Draw(subimg, subimg.Bounds(), img, image.Point{x, y}, draw.Src)
	return subimg
}

func visualizeMatches(img image.Image, matches []Match) image.Image {
	// Print points as rectangles of needle size
	output := image.NewRGBA(img.Bounds())
	draw.DrawMask(
		output, output.Bounds(),
		img, image.Point{},
		&image.Uniform{color.Alpha{20}}, image.Point{},
		draw.Over,
	)

	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]

		// Calculate the color based on match
		v := 1 - math.Min(1, (1-m.Match)*10)
		red := uint8(255 * (1 - v))
		green := uint8(255)
		blue := uint8(255 * (1 - v))
		color := color.RGBA{red, green, blue, 255}
		draw.Draw(output, m.Bounds, &image.Uniform{color}, image.Point{}, draw.Src)
	}
	return output
}

func pngb64(img image.Image) string {
	// Encode the image
	buffer := new(bytes.Buffer)
	err := png.Encode(buffer, img)
	if err != nil {
		log.Fatalf("failed to encode image: %v", err)
	}

	// Convert the bytes buffer to a base64 string
	encoded := base64.StdEncoding.EncodeToString(buffer.Bytes())

	return encoded
}

func convolution(targetImage image.Image, needleImage image.Image) image.Image {
	// Iterate over the target image and find the closest matches
	targetBounds := targetImage.Bounds()
	needleBounds := needleImage.Bounds()
	outputImage := image.NewRGBA(targetBounds)
	targetBounds.Max.X -= needleBounds.Max.X
	targetBounds.Max.Y -= needleBounds.Max.Y
	nw := needleBounds.Max.X
	nh := needleBounds.Max.Y
	narea := uint32(nw * nh)
	for y := targetBounds.Min.Y; y < targetBounds.Max.Y; y++ {
		for x := targetBounds.Min.X; x < targetBounds.Max.X; x++ {
			sum := sumOfAbsDiff(targetImage, x, y, needleImage)
			out := uint8(sum / uint32(3) / narea)
			out = 255 - out
			outputImage.Set(x, y, color.RGBA{out, out, out, 255})
		}
	}
	return outputImage
}

func convolutionParallel(img *image.RGBA, subimg *image.RGBA) image.Image {
	imgr := img.Bounds()
	subimgr := subimg.Bounds()
	outputImage := image.NewRGBA(imgr)

	imgr.Max.X -= subimgr.Max.X
	imgr.Max.Y -= subimgr.Max.Y

	nw := subimgr.Max.X
	nh := subimgr.Max.Y

	wg := sync.WaitGroup{}

	// Define the number of workers
	numWorkers := runtime.NumCPU() * 2

	// Calculate the height of each horizontal slice
	sliceHeight := imgr.Dy() / numWorkers

	norm := 1 / float64(nw*nh*3)

	// Launch workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			// Calculate the bounds for the current worker
			ya := workerID * sliceHeight
			yb := ya + sliceHeight
			// Make sure the last slice goes till the edge
			if workerID == numWorkers-1 {
				yb = imgr.Max.Y
			}

			xb := imgr.Max.X

			// Iterate over the target image slice
			for y := ya; y < yb; y++ {
				for x := 0; x < xb; x++ {
					// Perform the convolution operation
					sum := sumOfAbsDiffRGBA(img, x, y, subimg)
					out := 255 - uint8(float64(sum)*norm)
					outputImage.Set(x, y, color.RGBA{out, out, out, 255})
				}
			}

			// Signal that the worker has finished
			wg.Done()
		}(i)
	}

	// Wait for all workers to finish
	wg.Wait()

	return outputImage
}

func sumOfAbsDiff(img image.Image, x int, y int, subimg image.Image) uint32 {
	sum := uint32(0)
	b := subimg.Bounds()
	w := b.Dx()
	h := b.Dy()

	for ny := 0; ny < h; ny++ {
		for nx := 0; nx < w; nx++ {
			t := img.At(x+nx, y+ny)
			n := subimg.At(b.Min.X+nx, b.Min.Y+ny)
			sum += rgbAbsSum(t, n)
		}
	}
	return sum
}

func sumOfAbsDiffRGBA(img *image.RGBA, x int, y int, subimg *image.RGBA) uint32 {
	sum := uint32(0)
	b := subimg.Bounds()
	w := b.Dx()
	h := b.Dy()

	ipix := img.Pix
	spix := subimg.Pix

	for ny := 0; ny < h; ny++ {
		for nx := 0; nx < w; nx++ {
			i := img.PixOffset(x+nx, y+ny)
			j := subimg.PixOffset(b.Min.X+nx, b.Min.Y+ny)
			// sum += rgbAbsSumSliceBitwise(
			// 	ipix[i:i+4:i+4],
			// 	spix[j:j+4:j+4],
			// )
			sum += rgbAbsSumSliceBitwise(
				ipix[i:i+3:i+3],
				spix[j:j+3:j+3],
			)
		}
	}
	return sum
}

type Match struct {
	Bounds image.Rectangle `json:"bounds"`
	Match  float64         `json:"match"`
}

func (m Match) MarshalJSON() ([]byte, error) {
	type Bounds struct {
		X int `json:"x"`
		Y int `json:"y"`
		W int `json:"w"`
		H int `json:"h"`
	}
	return json.Marshal(struct {
		Bounds Bounds  `json:"bounds"`
		Match  float64 `json:"match"`
	}{
		Bounds: Bounds{
			X: m.Bounds.Min.X,
			Y: m.Bounds.Min.Y,
			W: m.Bounds.Dx(),
			H: m.Bounds.Dy(),
		},
		Match: m.Match,
	})
}

func convolutionTopK(img *image.RGBA, subimg *image.RGBA, k int) Matches {
	// Iterate over the target image and find the closest matches
	imgr := img.Bounds()
	subimgr := subimg.Bounds()
	subw := subimgr.Dx()
	subh := subimgr.Dy()

	inner := image.Rect(
		imgr.Min.X,
		imgr.Min.Y,
		imgr.Max.X-subw,
		imgr.Max.Y-subh,
	)

	var matches []Match
	var minSums []uint32
	// totalSum := 0.

	for y := inner.Min.Y; y < inner.Max.Y; y++ {
		for x := inner.Min.X; x < inner.Max.X; x++ {
			// Loop over needle
			// sum := uint32(0)
			sum := sumOfAbsDiffRGBA(img, x, y, subimg)
			// for ny := subimgr.Min.Y; ny < subimgr.Max.Y; ny++ {
			// 	for nx := subimgr.Min.X; nx < subimgr.Max.X; nx++ {
			// 		// Multiply corresponding pixels
			// 		targetPixel := img.At(x+nx, y+ny)
			// 		needlePixel := subimg.At(nx, ny)
			// 		// Pixel diff
			// 		sum += rgbAbsSum(targetPixel, needlePixel)
			// 	}
			// }

			// totalSum += float64(sum)
			bounds := image.Rect(x, y, x+subw, y+subh)

			// Check if the current match is one of the top k matches
			if len(matches) < k {
				matches = append(matches, Match{Bounds: bounds, Match: float64(sum)})
				minSums = append(minSums, sum)
			} else {
				maxDiffIndex := 0
				for i := 1; i < k; i++ {
					if minSums[i] > minSums[maxDiffIndex] {
						maxDiffIndex = i
					}
				}
				if sum < minSums[maxDiffIndex] {
					matches[maxDiffIndex] = Match{Bounds: bounds, Match: float64(sum)}
					minSums[maxDiffIndex] = sum
				}
			}
		}
	}

	norm := 1 / float64(subimgr.Max.X*subimgr.Max.Y*0xFF*3)
	for i := 0; i < len(matches); i++ {
		matches[i].Match = 1 - matches[i].Match*norm
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Match > matches[j].Match
	})

	return matches
}

func convolutionTopKParallel(img *image.RGBA, subimg *image.RGBA, k int) Matches {
	// Iterate over the target image and find the closest matches
	imgr := img.Bounds()
	subimgr := subimg.Bounds()
	subw := subimgr.Dx()
	subh := subimgr.Dy()

	inner := image.Rect(
		imgr.Min.X,
		imgr.Min.Y,
		imgr.Max.X-subw,
		imgr.Max.Y-subh,
	)

	if k < 1 {
		k = 1
	}

	numWorkers := runtime.NumCPU() * 2
	sliceHeight := inner.Dy() / numWorkers
	wg := sync.WaitGroup{}
	matchChan := make(chan Match)

	// Launch workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			// Calculate the bounds for the current worker
			ya := inner.Min.Y + workerID*sliceHeight
			yb := ya + sliceHeight
			// Make sure the last slice goes till the edge
			if workerID == numWorkers-1 {
				yb = inner.Dy()
			}

			xa := inner.Min.X
			xb := inner.Max.X

			var matches []Match
			var minSums []uint32
			// totalSum := 0.

			// Iterate over the target image slice
			for y := ya; y < yb; y++ {
				for x := xa; x < xb; x++ {
					// Perform the convolution operation
					sum := sumOfAbsDiffRGBA(img, x, y, subimg)
					bounds := image.Rect(x, y, x+subw, y+subh)

					// Check if the current match is one of the top k matches
					if len(matches) < k {
						matches = append(matches, Match{Bounds: bounds, Match: float64(sum)})
						minSums = append(minSums, sum)
					} else {
						maxDiffIndex := 0
						for i := 1; i < k; i++ {
							if minSums[i] > minSums[maxDiffIndex] {
								maxDiffIndex = i
							}
						}
						if sum < minSums[maxDiffIndex] {
							matches[maxDiffIndex] = Match{Bounds: bounds, Match: float64(sum)}
							minSums[maxDiffIndex] = sum
						}
					}
				}
			}

			// Send the matches to the channel
			for _, match := range matches {
				matchChan <- match
			}

			// Signal that the worker has finished
			wg.Done()
		}(i)
	}

	// Wait for all workers to finish and close the channel
	go func() {
		wg.Wait()
		close(matchChan)
	}()

	// Create a slice to store the matches
	matches := make([]Match, 0, k*numWorkers)

	// Read matches from the channel
	for match := range matchChan {
		matches = append(matches, match)
	}

	// Sort the matches
	sort.Slice(matches, func(i, j int) bool {
		// These are not matches, but rather the sum of absolute differences,
		// so we need to sort them in reverse order.
		return matches[i].Match < matches[j].Match
	})

	// Keep only the top k matches
	matches = matches[:k]

	// Normalize
	norm := 1 / float64(subimgr.Max.X*subimgr.Max.Y*0xFF*3)
	for i := 0; i < len(matches); i++ {
		matches[i].Match = 1 - matches[i].Match*norm
	}

	return matches
}

func rgbAbsSum(a, b color.Color) uint32 {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	var dr, dg, db uint32
	if ar > br {
		dr = ar - br
	} else {
		dr = br - ar
	}
	if ag > bg {
		dg = ag - bg
	} else {
		dg = bg - ag
	}
	if ab > bb {
		db = ab - bb
	} else {
		db = bb - ab
	}
	return (dr + dg + db) / 0xFF
}

func rgbAbsSumSlice(a, b []uint8) uint32 {
	ar, ag, ab := a[0], a[1], a[2]
	br, bg, bb := b[0], b[1], b[2]
	var dr, dg, db uint8
	if ar > br {
		dr = ar - br
	} else {
		dr = br - ar
	}
	if ag > bg {
		dg = ag - bg
	} else {
		dg = bg - ag
	}
	if ab > bb {
		db = ab - bb
	} else {
		db = bb - ab
	}
	return uint32(dr) + uint32(dg) + uint32(db)
}

func bitwiseAbsDiff(a, b uint8) uint32 {
	v := int32(a) - int32(b)
	m := v >> (32 - 1)
	return uint32((v + m) ^ m)
}

func rgbAbsSumSliceBitwise(a, b []uint8) uint32 {
	return bitwiseAbsDiff(a[0], b[0]) + bitwiseAbsDiff(a[1], b[1]) + bitwiseAbsDiff(a[2], b[2])
}

func multiplyPixels(pixel1 color.Color, pixel2 color.Color) color.Color {
	r1, g1, b1, a1 := pixel1.RGBA()
	r2, g2, b2, a2 := pixel2.RGBA()
	r := uint8((r1 * r2) >> 8)
	g := uint8((g1 * g2) >> 8)
	b := uint8((b1 * b2) >> 8)
	a := uint8((a1 * a2) >> 8)
	return color.RGBA{r, g, b, a}
}

func calculateMeanColor(img image.Image) color.Color {
	bounds := img.Bounds()
	var r, g, b, a uint32
	var count uint32

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := img.At(x, y)
			pr, pg, pb, pa := pixel.RGBA()
			r += pr
			g += pg
			b += pb
			a += pa
			count++
		}
	}

	r /= count
	g /= count
	b /= count
	a /= count

	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func openImage(filename string) (image.Image, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}

	return img, nil
}

func resizeImage(img image.Image, width, height int) *image.RGBA {
	bounds := img.Bounds()
	imgWidth := bounds.Max.X - bounds.Min.X
	imgHeight := bounds.Max.Y - bounds.Min.Y

	if width == 0 {
		width = int(float64(height) * float64(imgWidth) / float64(imgHeight))
	} else if height == 0 {
		height = int(float64(width) * float64(imgHeight) / float64(imgWidth))
	}

	if width < 1 {
		width = 1
	}

	if height < 1 {
		height = 1
	}

	resized := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Over, nil)
	return resized
}
