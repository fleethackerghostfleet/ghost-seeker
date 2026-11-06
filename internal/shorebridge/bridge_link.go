package shorebridge

func (b *Bridge) linkStatus() LinkStatus {
	ls := LinkStatus{}
	if b.link != nil {
		ls.Controllable, ls.Bps, ls.Bytes = true, b.link.Bps(), b.link.Bytes()
		ls.Distribution = b.link.Distribution()
		if s, ok := b.link.(LinkSetter); ok {
			ls.Fixed = s.Fixed()
		}
	}
	return ls
}
