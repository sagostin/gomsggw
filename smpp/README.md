# go-smpp

A complete implementation of SMPP v5 protocol, written in golang.

https://github.com/CursedHardware/go-smpp/tree/master

## Key features

- Message encoding auto-detection
- Multipart SMS automatic splitting and concatenating
- Supported encodings:

  ```plain
  UCS-2     GSM 7bit  ASCII      Latin-1
  Cyrillic  Hebrew    Shift-JIS  ISO-2022-JP
  EUC-JP    EUC-KR
  ```

## Caveats

- Please read [the SMPP Specification Version 5](https://github.com/CursedHardware/go-smpp/tree/master/docs/SMPP_v5.pdf) first. [pdu](https://github.com/CursedHardware/go-smpp/tree/master/pdu) is not limited to any value range.
- If you do not like the default [session.go](session.go) implementation, you can easily replace it.
- [Device-specific Caveats](https://github.com/CursedHardware/go-smpp/tree/master/docs/device-specific-caveats.md)

## Command line tools

1. [smpp-receiver](https://github.com/CursedHardware/go-smpp/tree/master/cmd/smpp-receiver)

   SMPP Simple Receiver tool

2. [smpp-repl](https://github.com/CursedHardware/go-smpp/tree/master/cmd/smpp-repl)

   SMPP Simple Test tool

## LICENSE

This piece of software is released under [the MIT license](https://github.com/CursedHardware/go-smpp/tree/master/LICENSE).
