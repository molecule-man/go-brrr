# Plot compression ratio vs speed from a cross-library benchmark CSV.
#
# Usage: gnuplot -p scripts/plot-crosslib.gnuplot
#        gnuplot -e "csv='bench-crosslib.csv'" scripts/plot-crosslib.gnuplot
#        gnuplot -e "csv='bench-crosslib.csv'; outfile='plot.png'" scripts/plot-crosslib.gnuplot

if (!exists("csv")) csv = 'bench-crosslib.csv'

if (exists("outfile")) {
    set terminal pngcairo size 600,600 enhanced
    set output outfile
}

set datafile separator ','
set key top right
set xlabel 'Speed (MB/s)'
set ylabel 'Compression Ratio (higher = better)'
set grid

if (exists("plottitle")) { set title plottitle noenhanced font ',14' }

# Extract unique library names by filtering rows.
# Label selected quality levels (q1, q6, q11) on the go-brrr line.
plot csv using 3:($0 > 0 && strcol(1) eq 'go-brrr'     ? $4 : 1/0) with linespoints pt 7 ps 1.2 title 'go-brrr', \
     csv using 3:($0 > 0 && strcol(1) eq 'go-brrr' && (strcol(2) eq '1' || strcol(2) eq '6' || strcol(2) eq '11') ? $4 : 1/0):(sprintf('q%s', strcol(2))) with labels offset char 1.2,0.8 font ',9' notitle, \
     csv using 3:($0 > 0 && strcol(1) eq 'zstd'         ? $4 : 1/0) with linespoints pt 9 ps 1.2 title 'zstd', \
     csv using 3:($0 > 0 && strcol(1) eq 'gzip'         ? $4 : 1/0) with linespoints pt 13 ps 1.2 title 'gzip'
