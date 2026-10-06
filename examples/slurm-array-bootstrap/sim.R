# Bootstrap the sampling distribution of a 10% trimmed mean.
# Each Slurm array task uses its own seed, so the tasks give different (independent) answers.
task <- as.integer(Sys.getenv("SLURM_ARRAY_TASK_ID", unset = NA))
if (is.na(task)) stop("SLURM_ARRAY_TASK_ID is not set: run this as an array task")
set.seed(1000 + task)
x <- rexp(200)
est <- replicate(2000, mean(sample(x, replace = TRUE), trim = 0.1))
cat("task", task, "mean", format(mean(est), digits = 6), "sd", format(sd(est), digits = 6), "\n")
