package blazeSDK

import (
	"fmt"
	"sort"
	"strings"
)

func formatPayload(payload interface{}) string {
	var sb strings.Builder
	sb.WriteString("BlazePayload ")
	writeValue(&sb, payload, 0)
	return sb.String()
}

// writeValue - appends a formatted rendering of one TDF value to sb
func writeValue(sb *strings.Builder, payload interface{}, indent int) {
	pad := strings.Repeat("  ", indent)
	switch payloadType := payload.(type) {

	case map[string]interface{}: // struct (TDF)
		sb.WriteString("{\n")
		for _, key := range sortedKeys(payloadType) {
			fmt.Fprintf(sb, "%s  %s = ", pad, key)
			writeValue(sb, payloadType[key], indent+1)
			sb.WriteString("\n")
		}
		fmt.Fprintf(sb, "%s}", pad)

	case []interface{}: // list
		sb.WriteString("[\n")
		for i, key := range payloadType {
			fmt.Fprintf(sb, "%s  [%d] = ", pad, i)
			writeValue(sb, key, indent+1)
			sb.WriteString("\n")
		}
		fmt.Fprintf(sb, "%s]", pad)

	case map[interface{}]interface{}: // map
		sb.WriteString("[\n")
		for _, key := range sortedMapKeys(payloadType) {
			fmt.Fprintf(sb, "%s  (%s, ", pad, scalar(key))
			writeValue(sb, payloadType[key], indent+1)
			sb.WriteString(")\n")
		}
		fmt.Fprintf(sb, "%s]", pad)

	default: // scalar
		sb.WriteString(scalar(payload))
	}
}

// scalar - prints the formatted output of the basic TDF types
func scalar(v interface{}) string {
	switch x := v.(type) {
	case int64:
		return fmt.Sprintf("%d (0x%04X)", x, uint64(x))
	case string:
		return fmt.Sprintf("%q", x)
	case []byte:
		return fmt.Sprintf("0x%X", x) // blob
	case float32:
		return fmt.Sprintf("%g", x)
	default: // ObjectType/ObjectID/Union/Variable
		return fmt.Sprintf("%v", x)
	}
}

// sortedKeys - sorts the values of a LIST
func sortedKeys(input map[string]interface{}) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedKeys - sorts the values of a MAP
func sortedMapKeys(input map[interface{}]interface{}) []interface{} {
	keys := make([]interface{}, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprintf("%v", keys[i]) < fmt.Sprintf("%v", keys[j])
	})
	return keys
}
