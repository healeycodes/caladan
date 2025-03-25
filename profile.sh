#!/bin/bash

# Function to run profiling
run_profile() {
  profile_name=$1
  tar_workers=$2
  
  echo "Running profile: $profile_name (TAR_WORKERS=$tar_workers)"
  
  # Set environment variables
  export CPU_PROFILE="${profile_name}.prof"
  if [ -n "$tar_workers" ]; then
    export TAR_WORKERS="$tar_workers"
  else
    unset TAR_WORKERS
  fi
  
  ./benchmark-caladan.sh
  
  # Generate the text report from the profile
  go tool pprof -text "$CPU_PROFILE" > "${profile_name}.txt"
  
  echo "Created profile at $CPU_PROFILE and text report at ${profile_name}.txt"
}

# Run profiles with different configurations
run_profile "cpu_with_sema" ""
run_profile "cpu_without_sema" "1000"

echo "Profiling complete."
