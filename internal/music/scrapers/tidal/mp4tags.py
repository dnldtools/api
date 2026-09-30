import argparse
from mutagen.mp4 import MP4, MP4FreeForm

ap = argparse.ArgumentParser()
ap.add_argument("file")
ap.add_argument("--isrc")
ap.add_argument("--copyright")
args = ap.parse_args()

audio = MP4(args.file)
if args.isrc:
    audio["----:com.apple.iTunes:ISRC"] = [MP4FreeForm(args.isrc.encode())]
if args.copyright:
    audio["cprt"] = [args.copyright]
audio.save()
