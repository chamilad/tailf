# Tail implemented with Go 

.. for the thousandth time.. 

not for production, not supported, yada yada

Based on the wonderful talk done by Fabian Stäber at [Implementing 'tail -f' in Go](https://www.youtube.com/watch?v=lLDWF59aZAo)

## Compatibility
Works only on Linux, tested only on Ubuntu with an 88/256 terminal

## Usage
#### Tail a single file, starting with the last 5 lines
```bash
$ tailf -5 someserver.log
```

#### Tail multiple files
```bash
$ tailf someserver.log someserver-access.log someserver-error.log
```
When tailing multiple files, the filename will be prefixed to the output

## License
Apache v2 
