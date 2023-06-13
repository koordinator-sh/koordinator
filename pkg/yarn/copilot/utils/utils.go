package utils

func DiffMap(m1, m2 map[string]struct{}) (m1More, m1Less map[string]struct{}) {
	m1More = map[string]struct{}{}
	m1Less = map[string]struct{}{}
	for k1, _ := range m1 {
		if _, ok := m2[k1]; !ok {
			m1More[k1] = struct{}{} // key 仅在第一个 map 中存在
		}
	}

	for k2, _ := range m2 {
		if _, ok := m1[k2]; !ok {
			m1Less[k2] = struct{}{} // key 仅在第二个 map 中存在
		}
	}

	return
}
