package get

import (
	"bufio"
	"io"
	"os"
	"path"
)

// Storage allows to store data in a local directory
type Storage struct {
	directory string
}

// NewStorage returns a new Storage given a local directory
func NewStorage(directory string) *Storage {
	return &Storage{directory}
}

// NewStoringReader returns a reader that will also store any read data to filename
func (s *Storage) NewStoringReader(filename string, reader io.ReadCloser) (result io.ReadCloser, err error) {
	file, err := os.Create(path.Join(s.directory, filename))
	if err != nil {
		return
	}

	writer := bufio.NewWriter(file)
	teeReader := io.TeeReader(reader, writer)

	result = &storingReader{reader, writer, teeReader}
	return
}

// storingReader uses a TeeReader to copy data from a reader to a writer
type storingReader struct {
	reader    io.ReadCloser
	writer    *bufio.Writer
	teeReader io.Reader
}

// Read delegates to the TeeReader implementation
func (t *storingReader) Read(p []byte) (n int, err error) {
	return t.teeReader.Read(p)
}

// Closes the internal reader and flushes the writer
func (t *storingReader) Close() (err error) {
	err = t.reader.Close()
	if err != nil {
		return
	}
	return t.writer.Flush()
}