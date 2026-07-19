package pipeline

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func parseSpriteSizes(values []string) ([]spriteSize, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one sprite size is required")
	}
	result := make([]spriteSize, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		left, right, ok := strings.Cut(value, "x")
		if !ok {
			return nil, fmt.Errorf("invalid sprite size %q", value)
		}
		width, err := strconv.Atoi(left)
		if err != nil {
			return nil, fmt.Errorf("invalid sprite width %q", left)
		}
		height, err := strconv.Atoi(right)
		if err != nil {
			return nil, fmt.Errorf("invalid sprite height %q", right)
		}
		if width <= 0 || height <= 0 {
			return nil, fmt.Errorf("sprite size %q must be positive", value)
		}
		result = append(result, spriteSize{Name: fmt.Sprintf("%dx%d", width, height), Width: width, Height: height})
	}
	return result, nil
}

func spriteMediaID(size spriteSize) string {
	return spriteMediaIDForSize(size.Width, size.Height)
}

func spriteMediaIDForSize(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	return fmt.Sprintf("sprite_%dx%d", width, height)
}

func spriteSizeGroups(sizes []spriteSize) [][]spriteSize {
	byRatio := make(map[string][]spriteSize)
	for _, size := range sizes {
		divisor := gcd(size.Width, size.Height)
		key := fmt.Sprintf("%d:%d", size.Width/divisor, size.Height/divisor)
		byRatio[key] = append(byRatio[key], size)
	}
	keys := make([]string, 0, len(byRatio))
	for key := range byRatio {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	groups := make([][]spriteSize, 0, len(keys))
	for _, key := range keys {
		group := byRatio[key]
		sort.Slice(group, func(i, j int) bool {
			leftArea := group[i].Width * group[i].Height
			rightArea := group[j].Width * group[j].Height
			if leftArea == rightArea {
				return group[i].Name < group[j].Name
			}
			return leftArea > rightArea
		})
		groups = append(groups, group)
	}
	return groups
}

func gcd(left, right int) int {
	for right != 0 {
		left, right = right, left%right
	}
	if left < 0 {
		return -left
	}
	if left == 0 {
		return 1
	}
	return left
}
