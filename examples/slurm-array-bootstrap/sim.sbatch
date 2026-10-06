#!/bin/bash
#SBATCH --job-name=boot
#SBATCH --cpus-per-task=1
#SBATCH --mem=2G
#SBATCH --time=00:20:00
#SBATCH --array=0-9
module load R
Rscript sim.R
