# Horizontal bar chart of speed from a cross-library benchmark .dat file.
#
# Usage: gnuplot -e "dat='file.dat'; outfile='plot.png'; plottitle='...'; ytics_str='...'" scripts/plot-crosslib-bars.gnuplot

if (!exists("dat")) dat = 'bench-crosslib.dat'

if (exists("outfile")) {
    if (outfile[strlen(outfile)-3:strlen(outfile)] eq '.svg') {
        set terminal svg size 600,600 enhanced background rgb 'white'
    } else {
        set terminal pngcairo size 600,600 enhanced
    }
    set output outfile
}

if (exists("plottitle")) { set title plottitle noenhanced font ',14' }

set style fill solid 0.8 border -1
set boxwidth 0.7
set xlabel 'Speed (MB/s)'
set ylabel ''
set grid x
set xrange [0:*]
set yrange [] reverse
set key bottom right
# Room right of the longest bar for its value label.
set offsets 0, graph 0.08, 0, 0

eval sprintf("set ytics (%s)", ytics_str)

# col1=row, col2=speed, col3=lib_index (1=go-brrr, 2=zstd, 3=gzip)
# Colors match plot-crosslib.gnuplot.
set linetype 1 lc rgb '#9400d3'
set linetype 2 lc rgb '#2a8cc8'
set linetype 3 lc rgb '#e69f00'

plot dat using 2:0:(0):2:($0-0.35):($0+0.35):3 with boxxyerror lc variable notitle, \
     dat using 2:0:(sprintf('%.0f', $2)) with labels left offset char 0.5,0 font ',8' notitle, \
     NaN with boxes fs solid 0.8 lc rgb '#9400d3' title 'go-brrr', \
     NaN with boxes fs solid 0.8 lc rgb '#2a8cc8' title 'zstd', \
     NaN with boxes fs solid 0.8 lc rgb '#e69f00' title 'gzip'
