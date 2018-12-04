package cmd

import (
	"compress/bzip2"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/zstd"
	"github.com/buger/jsonparser"
	"github.com/cheggaaa/pb"
	"github.com/fiws/minepkg/internals/curse"
)

const dbPath = "https://clientupdate-v6.cursecdn.com/feed/addons/432/v10/complete.json.bz2"
const fileName = "complete.json.zst"

type modDB struct {
	Mods []curse.Mod `json:"data"`
}

func (m *modDB) modByID(id uint32) *curse.Mod {
	for _, mod := range m.Mods {
		if mod.ID == id {
			return &mod
		}
	}
	return nil
}

func (m *modDB) modBySlug(slug string) *curse.Mod {
	for _, mod := range m.Mods {
		if mod.Slug == slug {
			return &mod
		}
	}
	return nil
}

func refreshDb() {
	infoColor.Println("🚛 Updating local mod database.")

	res, err := http.Get(dbPath)
	if err != nil {
		fmt.Println(err)
	}

	dbSize, _ := strconv.Atoi(res.Header.Get("content-length"))
	if dbSize == 0 {
		dbSize = 15500000 // we estimate db to be 15MB if there is no header 😅
	}
	bar := pb.New(dbSize).SetUnits(pb.U_BYTES)
	bar.SetRefreshRate(time.Millisecond * 20)
	bar.Start()

	// 1. proxy the body to display pogress
	proxy := bar.NewProxyReader(res.Body)

	// 2. decompress the response (bzip2)
	decompressor := bzip2.NewReader(proxy)

	if err := os.MkdirAll(globalDir, 0755); err != nil {
		panic(err)
	}

	targetPath := filepath.Join(globalDir, fileName)
	// 3. compress it again using zst and write it to our destination file
	destinationFile, err := os.Create(targetPath)
	if err != nil {
		fmt.Println(err)
	}
	compressor := zstd.NewWriter(destinationFile)

	// copy from decompressor (bzip2 from http) to compressor (zst in file)
	io.Copy(compressor, decompressor)
	compressor.Close()
	bar.Finish()
	successColor.Println(" ✔ Updated local db.")
}

func parseDb(b []byte) *modDB {
	db := modDB{}

	jsonparser.ArrayEach(b, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		name, _ := jsonparser.GetString(value, "Name")
		url, _ := jsonparser.GetString(value, "WebSiteURL")
		packageType, _ := jsonparser.GetInt(value, "PackageType")
		id, _ := jsonparser.GetInt(value, "Id")
		rawDl, dataType, _, _ := jsonparser.Get(value, "DownloadCount")

		parsed, _ := strconv.ParseFloat(string(rawDl), 32)
		downloadCount := float32(parsed)
		urlParts := strings.Split(url, "/")

		slug := urlParts[len(urlParts)-1]

		db.Mods = append(db.Mods, curse.Mod{
			Name:          name,
			Slug:          slug,
			ID:            uint32(id),
			DownloadCount: downloadCount,
			PackageType:   uint8(packageType),
		})
	}, "data")

	return &db
}

func readDbOrDownload() *modDB {
	file, err := ioutil.ReadFile(filepath.Join(globalDir, fileName))
	if err != nil {
		fmt.Println("There is no local mod db yet! Downloading now …")
		err = nil
		refreshDb()
		file, err = ioutil.ReadFile(filepath.Join(globalDir, fileName))
		if err != nil {
			fmt.Println("Refreshing DB failed: " + err.Error())
			os.Exit(-1)
		}
	}

	uncompressed, _ := zstd.Decompress(nil, file)
	return parseDb(uncompressed)
}
