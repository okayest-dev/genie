package plugin

import (
	"bufio"
	"encoding/json"
	"io"
)

type Codec struct {
	reader *bufio.Reader
	writer *bufio.Writer
}

func NewCodec(r io.Reader, w io.Writer) *Codec {
	return &Codec{
		reader: bufio.NewReader(r),
		writer: bufio.NewWriter(w),
	}
}

func (c *Codec) WriteRequest(req *Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := c.writer.Write(data); err != nil {
		return err
	}
	if err := c.writer.WriteByte('\n'); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *Codec) WriteResponse(resp *Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if _, err := c.writer.Write(data); err != nil {
		return err
	}
	if err := c.writer.WriteByte('\n'); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *Codec) ReadRequest() (*Request, error) {
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, io.EOF
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func (c *Codec) ReadResponse() (*Response, error) {
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, io.EOF
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}