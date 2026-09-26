"""Writes the reference encodings the Go tests compare against (made with Python kbinxml 2.1).

  python testdata/make_reference.py

<fixture>.kbin   KBinXML(<fixture>.xml).to_binary()            (Shift-JIS, six-bit names)
services.kbin    a services.get response with every value type the tracker rewrites
types.kbin       one node of each value type, including arrays and odd-sized packing
raw_musicreg.xml the music.reg element as the Python tracker stored it (lxml tostring)
"""

import os
import sys

from kbinxml import KBinXML
from lxml import etree

HERE = os.path.dirname(os.path.abspath(__file__))

SERVICES = ('<response><services expire="10800" method="get" mode="operation" status="0">'
            '<item name="ntp" url="ntp://pool.ntp.org/"/>'
            '<item name="keepalive" url="http://127.0.0.1/core/keepalive?pa=127.0.0.1"/>'
            '<item name="cardmng" url="http://10.0.0.5:8083"/><item name="local" url="http://10.0.0.5:8083/core"/>'
            '</services></response>')

# packing of 1- and 2-byte values shares 4-byte slots; this mix exercises every path
TYPES = ('<root a="x" b="日本語">'
         '<u8 __type="u8">255</u8><s8 __type="s8">-1</s8><u16 __type="u16">65535</u16>'
         '<s16 __type="s16">-2</s16><u8b __type="u8">7</u8b><s32 __type="s32">-123456</s32>'
         '<u32 __type="u32">4000000000</u32><s64 __type="s64">-9000000000</s64>'
         '<u64 __type="u64">18000000000000000000</u64><bool __type="bool">1</bool>'
         '<ip __type="ip4">192.168.1.5</ip><t __type="time">1700000000</t>'
         '<f __type="float">1.500000</f><d __type="double">-2.250000</d>'
         '<v2 __type="2u8">1 2</v2><v3 __type="3s16">-1 0 1</v3><v4 __type="4u32">1 2 3 4</v4>'
         '<arr __type="s32" __count="3">-1 2 -3</arr><arr8 __type="u8" __count="5">1 2 3 4 5</arr8>'
         '<b __type="bin" __size="3">00ff10</b><s __type="str">シャッフル&amp;&lt;x&gt;</s><e __type="str"></e>'
         '<plain>text without type</plain><void/><deep><inner x="1"/></deep>'
         '</root>')


def main():
    for name, xml in (("services", SERVICES), ("types", TYPES)):
        with open(os.path.join(HERE, name + ".xml"), "wb") as f:
            f.write(xml.encode("utf-8"))
    for name in sorted(os.listdir(HERE)):
        if name.endswith(".xml") and not name.startswith("raw_"):
            with open(os.path.join(HERE, name), "rb") as f:
                data = KBinXML(f.read()).to_binary()
            with open(os.path.join(HERE, name[:-4] + ".kbin"), "wb") as f:
                f.write(data)
    with open(os.path.join(HERE, "musicreg_req.kbin"), "rb") as f:
        m = KBinXML(f.read()).xml_doc[0]
    with open(os.path.join(HERE, "raw_musicreg.xml"), "wb") as f:
        f.write(etree.tostring(m))
    print("ok", file=sys.stderr)


if __name__ == "__main__":
    main()
