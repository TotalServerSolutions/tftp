package tftp

import (
	"net"
	"io"
	"fmt"
	"time"
)

type sender struct {
	conf config
	remoteAddr *net.UDPAddr
	conn *net.UDPConn
	reader *io.PipeReader
	filename string
	mode string
}

func (s *sender) Run(isServerMode bool) {
	var buffer, tmp []byte
	buffer = make([]byte, BLOCK_SIZE)
	tmp = make([]byte, MAX_DATAGRAM_SIZE)
	if !isServerMode {
		e := s.sendRequest(tmp)
		if e != nil {
			s.conf.Log().Printf("Error starting transmission: %v", e)
			s.reader.CloseWithError(e)
			return
		}
	}
	var blockNumber uint16
	blockNumber = 1
	lastBlockSize := -1
	for {
		c, readError := s.reader.Read(buffer)
		if readError != nil {
			if readError == io.EOF { // && c == 0 ?
				// we could have c != 0 here
				if c != 0 {
					panic("error!")
				}
				if lastBlockSize == BLOCK_SIZE || lastBlockSize == -1 {
					sendError := s.sendBlock(buffer, 0, blockNumber, tmp)
					if sendError != nil {
						s.conf.Log().Printf("Error sending last block: %v", sendError)
					}
				}
			} else {
				s.conf.Log().Printf("Handler error: %v", readError)
				errorPacket := ERROR{1, readError.Error()}
				s.conn.WriteToUDP(errorPacket.Pack(), s.remoteAddr)
				s.conf.Log().Printf("sent ERROR (code=%d): %s", 1, readError.Error())
			}
			return
		}
		if c == 0 {
			continue;
		}
		sendError := s.sendBlock(buffer, c, blockNumber, tmp)
		if sendError != nil {
			s.conf.Log().Printf("Error sending block %d: %v", blockNumber, sendError)
			s.reader.CloseWithError(sendError)
			return
		}
		blockNumber++
		lastBlockSize = c
	}
}

func (s *sender) sendRequest(tmp []byte) (e error) {
	for i := 0; i < s.conf.RetryCount(); i++ {
		wrqPacket := WRQ{s.filename, s.mode}
		s.conn.WriteToUDP(wrqPacket.Pack(), s.remoteAddr)
		s.conf.Log().Printf("sent WRQ (filename=%s, mode=%s)", s.filename, s.mode)
		setDeadlineError := s.conn.SetReadDeadline(time.Now().Add(time.Duration(s.conf.Timeout()) * time.Second))
		if setDeadlineError != nil {
			return fmt.Errorf("Could not set UDP timeout: %v", setDeadlineError)
		}
		for {
			c, remoteAddr, readError := s.conn.ReadFromUDP(tmp)
			if networkError, ok := readError.(net.Error); ok && networkError.Timeout() {
				break
			} else if readError != nil {
				return fmt.Errorf("Error reading UDP packet: %v", readError)
			}
			packet, e := ParsePacket(tmp[:c])
			if e != nil {
				continue
			}
			switch p := Packet(*packet).(type) {
				case *ACK:
					if p.BlockNumber == 0 {
						s.conf.Log().Printf("got ACK #0");
						s.remoteAddr = remoteAddr
						return nil
					}
				case *ERROR:
					return fmt.Errorf("Transmission error %d: %s", p.ErrorCode, p.ErrorMessage)
			}
		}	
	}
	return fmt.Errorf("Send timeout")
}

func (s *sender) sendBlock(b []byte, c int, n uint16, tmp []byte) (e error) {
	for i := 0; i < s.conf.RetryCount(); i++ {
		setDeadlineError := s.conn.SetReadDeadline(time.Now().Add(time.Duration(s.conf.Timeout()) * time.Second))
		if setDeadlineError != nil {
			return fmt.Errorf("Could not set UDP timeout: %v", setDeadlineError)
		}
		dataPacket := DATA{n, b[:c]}
		s.conn.WriteToUDP(dataPacket.Pack(), s.remoteAddr)
		s.conf.Log().Printf("sent DATA #%d (%d bytes)", n, c)
		for {
			c, _, readError := s.conn.ReadFromUDP(tmp)
			if networkError, ok := readError.(net.Error); ok && networkError.Timeout() {
				break
			} else if readError != nil {
				return fmt.Errorf("Error reading UDP packet: %v", readError)
			}
			packet, e := ParsePacket(tmp[:c])
			if e != nil {
				continue
			}
			switch p := Packet(*packet).(type) {
				case *ACK:
					s.conf.Log().Printf("got ACK #%d", p.BlockNumber)
					if n == p.BlockNumber {
						return nil
					}
				case *ERROR:
					return fmt.Errorf("Transmission error %d: %s", p.ErrorCode, p.ErrorMessage)
			}
		}	
	}
	return fmt.Errorf("Send timeout")
}
