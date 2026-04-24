# Horizontal bar chart of speed from a cross-library benchmark .dat file.
#
# Usage: gnuplot -e "dat='file.dat'; outfile='plot.png'; plottitle='...'; ytics_str='...'" scripts/plot-crosslib-bars.gnuplot

if (!exists("dat")) dat = 'bench-crosslib.dat'

if (exists("outfile")) {
    set terminal pngcairo size 600,600 enhanced
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

eval sprintf("set ytics (%s)", ytics_str)

# col1=row, col2=speed, col3=lib_index (1=go-brrr, 2=zstd, 3=gzip)
# Color palette: go-brrr=purple (#9467bd), zstd=cyan (#17becf), gzip=orange (#ff7f0e)
set linetype 1 lc rgb '#9467bd'
set linetype 2 lc rgb '#17becf'
set linetype 3 lc rgb '#ff7f0e'

plot dat using 2:0:(0):2:($0-0.35):($0+0.35):3 with boxxyerror lc variable notitle, \
     NaN with boxes fs solid 0.8 lc rgb '#9467bd' title 'go-brrr', \
     NaN with boxes fs solid 0.8 lc rgb '#17becf' title 'zstd', \
     NaN with boxes fs solid 0.8 lc rgb '#ff7f0e' title 'gzip'
