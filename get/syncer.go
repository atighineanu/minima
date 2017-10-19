package get

import (
	"compress/gzip"
	"encoding/xml"
	"io"
	"log"
	"strings"

	"github.com/moio/minima/util"
)

// common

// XMLLocation maps a <location> tag in repodata/repomd.xml or repodata/<ID>-primary.xml.gz
type XMLLocation struct {
	Href string `xml:"href,attr"`
}

// repodata/repomd.xml

// XMLRepomd maps a <repomd> tag in repodata/repomd.xml
type XMLRepomd struct {
	Data []XMLData `xml:"data"`
}

// XMLData maps a <data> tag in repodata/repomd.xml
type XMLData struct {
	Type     string      `xml:"type,attr"`
	Location XMLLocation `xml:"location"`
}

// repodata/<ID>-primary.xml.gz

// XMLMetaData maps a <metadata> tag in repodata/<ID>-primary.xml.gz
type XMLMetaData struct {
	Packages []XMLPackage `xml:"package"`
}

// XMLPackage maps a <package> tag in repodata/<ID>-primary.xml.gz
type XMLPackage struct {
	Arch     string      `xml:"arch"`
	Location XMLLocation `xml:"location"`
	Checksum XMLChecksum `xml:"checksum"`
}

// XMLChecksum maps a <checksum> tag in repodata/<ID>-primary.xml.gz
type XMLChecksum struct {
	Type     string `xml:"type,attr"`
	Checksum string `xml:",cdata"`
}

var checksumTypeMap = map[string]ChecksumType{
	"sha":    SHA1,
	"sha1":   SHA1,
	"sha256": SHA256,
}

const repomdPath = "repodata/repomd.xml"

// Syncer syncs repos from an HTTP source to a Storage
type Syncer struct {
	// URL of the repo this syncer syncs
	Url     string
	archs   map[string]bool
	storage Storage
}

// NewSyncer creates a new Syncer
func NewSyncer(url string, archs map[string]bool, storage Storage) *Syncer {
	return &Syncer{url, archs, storage}
}

// StoreRepo stores an HTTP repo in a Storage
func (r *Syncer) StoreRepo() (err error) {
	packagesToDownload, packagesToRecycle, err := r.processMetadata()
	if err != nil {
		return
	}

	downloadCount := len(packagesToDownload)
	log.Printf("Downloading %v packages...\n", downloadCount)
	for _, pack := range packagesToDownload {
		err = r.downloadStoreApply(pack.Location.Href, pack.Checksum.Checksum, util.Nop)
		if err != nil {
			return
		}
	}

	recycleCount := len(packagesToRecycle)
	log.Printf("Recycling %v packages...\n", recycleCount)
	for _, pack := range packagesToRecycle {
		err = r.storage.Recycle(pack.Location.Href)
		if err != nil {
			return
		}
	}

	log.Printf("Committing changes...\n")
	err = r.storage.Commit()
	if err != nil {
		return
	}
	return
}

// downloadStore downloads a repo-relative path into a file
func (r *Syncer) downloadStore(path string) error {
	return r.downloadStoreApply(path, "", util.Nop)
}

// downloadStoreApply downloads a repo-relative path into a file, while applying a ReaderConsumer
func (r *Syncer) downloadStoreApply(path string, checksum string, f util.ReaderConsumer) error {
	log.Printf("Downloading %v...", path)
	return DownloadApply(r.Url+"/"+path, util.Compose(r.storage.StoringMapper(path, checksum), f))
}

// processMetadata stores the repo metadata and returns a list of package file
// paths to download
func (r *Syncer) processMetadata() (packagesToDownload []XMLPackage, packagesToRecycle []XMLPackage, err error) {
	err = r.downloadStoreApply(repomdPath, "", func(reader io.ReadCloser) (err error) {
		decoder := xml.NewDecoder(reader)
		var repomd XMLRepomd
		err = decoder.Decode(&repomd)
		if err != nil {
			return
		}

		data := repomd.Data
		for i := 0; i < len(data); i++ {
			metadataPath := data[i].Location.Href
			if data[i].Type == "primary" {
				packagesToDownload, packagesToRecycle, err = r.processPrimary(metadataPath)
			} else {
				err = r.downloadStore(metadataPath)
			}
			if err != nil {
				return
			}
		}
		return
	})
	if err != nil {
		return
	}

	err = r.downloadStore(repomdPath + ".asc")
	if err != nil {
		if strings.HasSuffix(err.Error(), "404") {
			log.Printf("Got 404, ignoring...")
			err = nil
		} else {
			return
		}
	}

	err = r.downloadStore(repomdPath + ".key")
	if err != nil {
		if strings.HasSuffix(err.Error(), "404") {
			log.Printf("Got 404, ignoring...")
			err = nil
		} else {
			return
		}
	}

	return
}

// processPrimary stores the primary XML metadata file and returns a list of
// package file paths to download
func (r *Syncer) processPrimary(path string) (packagesToDownload []XMLPackage, packagesToRecycle []XMLPackage, err error) {
	err = r.downloadStoreApply(path, "", func(reader io.ReadCloser) (err error) {
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return
		}
		defer gzReader.Close()

		decoder := xml.NewDecoder(gzReader)
		var primary XMLMetaData
		err = decoder.Decode(&primary)
		if err != nil {
			return
		}

		allArchs := len(r.archs) == 0
		for _, pack := range primary.Packages {
			if allArchs || pack.Arch == "noarch" || r.archs[pack.Arch] {
				storageChecksum, err := r.storage.Checksum(pack.Location.Href, checksumTypeMap[pack.Checksum.Type])
				switch {
				case err == ErrFileNotFound:
					log.Printf("...package '%v' not found, will be downloaded\n", pack.Location.Href)
					packagesToDownload = append(packagesToDownload, pack)
				case err != nil:
					log.Printf("Checksum evaluation of the package '%v' returned the following error:\n", pack.Location.Href)
					log.Printf("Error message: %v\n", err)
					log.Println("...package skipped")
				case pack.Checksum.Checksum != storageChecksum:
					log.Printf("...package '%v' has a checksum error, will be redownloaded\n", pack.Location.Href)
					log.Printf("[repo vs local] = ['%v' VS '%v']\n", pack.Checksum.Checksum, storageChecksum)
					packagesToDownload = append(packagesToDownload, pack)
				default:
					log.Printf("...package '%v' is up-to-date already, will be recycled\n", pack.Location.Href)
					packagesToRecycle = append(packagesToRecycle, pack)
				}
			}
		}
		return
	})
	return
}
