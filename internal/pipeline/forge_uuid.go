package pipeline

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"os"

	"forge_worker/internal/task"
)

var xmpUUIDBoxID = [16]byte{0xbe, 0x7a, 0xcf, 0xcb, 0x97, 0xa9, 0x42, 0xe8, 0x9c, 0x71, 0x99, 0x94, 0x91, 0xe3, 0xaf, 0xac}

func appendForgeUUIDTags(request task.Request, paths []string, code task.ErrorCode) error {
	if len(paths) == 0 {
		return nil
	}
	for _, path := range paths {
		if err := appendForgeUUIDXMPBox(path, request.TaskUUID); err != nil {
			return task.NewError(code, err.Error(), true)
		}
	}
	return nil
}

func appendForgeUUIDXMPBox(path, uuid string) error {
	payload := []byte(forgeUUIDXMPPacket(uuid))
	boxSize := 8 + len(xmpUUIDBoxID) + len(payload)
	if uint64(boxSize) > uint64(^uint32(0)) {
		return fmt.Errorf("forge_uuid XMP box is too large for %s", path)
	}
	var header [24]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(boxSize))
	copy(header[4:8], "uuid")
	copy(header[8:24], xmpUUIDBoxID[:])

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open file for forge_uuid metadata: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(header[:]); err != nil {
		return fmt.Errorf("write forge_uuid XMP box header: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		return fmt.Errorf("write forge_uuid XMP box payload: %w", err)
	}
	return nil
}

func forgeUUIDXMPPacket(uuid string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(uuid))
	return `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/" x:xmptk="emos_forge_worker">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about="" xmlns:forge="https://forge.emos.best/" forge:forge_uuid="` + escaped.String() + `"/>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>
`
}
